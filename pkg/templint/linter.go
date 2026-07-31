package templint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Linter applies rules to .templ files and collects diagnostics.
type Linter struct {
	rules []Rule
}

// ruleDef holds the static metadata for a single lint rule.
type ruleDef struct {
	ctor       func(Severity) Rule
	defaultSev Severity
}

// rules is the single registry of every known rule, keyed by ID. Both the
// constructor and the recommended default severity live in one entry to
// prevent the two from drifting out of sync.
var rules = map[string]ruleDef{
	"inline-if":              {func(s Severity) Rule { return &inlineIfRule{severity: s} }, Error},
	"inline-for":             {func(s Severity) Rule { return &inlineForRule{severity: s} }, Error},
	"inline-switch":          {func(s Severity) Rule { return &inlineSwitchRule{severity: s} }, Error},
	"no-native-form-actions": {func(s Severity) Rule { return &noNativeFormActionsRule{severity: s} }, Error},
	"htmx-conflict":          {func(s Severity) Rule { return &htmxConflictRule{severity: s} }, Error},
	"img-alt":                {func(s Severity) Rule { return &imgAltRule{severity: s} }, Warning},
	"no-href":                {func(s Severity) Rule { return &noHrefRule{severity: s} }, Warning},
	"inline-style":           {func(s Severity) Rule { return &inlineStyleRule{severity: s} }, Warning},
	"empty-class":            {func(s Severity) Rule { return &emptyClassRule{severity: s} }, Warning},
	"js-href":                {func(s Severity) Rule { return &jsHrefRule{severity: s} }, Warning},
}

// New creates a Linter configured by the given Config. Only rules listed in
// cfg.Rules are enabled; passing nil or an empty Config yields a linter that
// reports nothing.
func New(cfg *Config) *Linter {
	if cfg == nil || len(cfg.Rules) == 0 {
		return &Linter{}
	}
	var active []Rule
	for _, id := range AllRuleIDs() {
		sev, ok := cfg.Rules[id]
		if !ok {
			continue
		}
		active = append(active, rules[id].ctor(sev))
	}
	return &Linter{rules: active}
}

// RuleCount returns the number of rules registered on this Linter (the number
// of rules that will run on each LintFile call).
func (l *Linter) RuleCount() int { return len(l.rules) }

// LintFile lints a single file and returns diagnostics.
func (l *Linter) LintFile(path string) ([]Diagnostic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	var diags []Diagnostic
	for _, rule := range l.rules {
		diags = append(diags, rule.Check(path, lines)...)
	}
	diags = l.applySuppressions(path, lines, diags)

	sort.Slice(diags, func(i, j int) bool {
		if diags[i].Line != diags[j].Line {
			return diags[i].Line < diags[j].Line
		}
		return diags[i].Col < diags[j].Col
	})

	return diags, nil
}

// ignoreDirectiveRe matches a `templint:ignore` directive inside a `//`
// comment. The submatch is everything after the directive keyword: the
// optional comma-separated rule list and the optional ` -- reason`.
var ignoreDirectiveRe = regexp.MustCompile(`//\s*templint:ignore\b(.*)$`)

// suppression is one parsed `templint:ignore` directive.
type suppression struct {
	dirLine int      // 1-based line the directive itself sits on
	col     int      // 1-based column of the `//`
	target  int      // 1-based line whose diagnostics it suppresses
	ruleIDs []string // empty means "every rule"
}

// parseSuppressions extracts every `templint:ignore` directive from the file.
//
// A directive on a line of its own targets the line below it; a directive
// trailing source content targets the line it sits on. Only those two lines
// are ever considered — there is no cascading lookback, so of two stacked
// comment directives only the adjacent one applies.
//
// Because rules anchor their diagnostics to the line a tag *opens* on, a
// directive for a multi-line tag goes directly above the opening `<form`,
// not above the offending attribute further down.
//
// Rules run over the whole file including comments, so a reason string must
// not contain markup a rule matches — an own-line directive targets the line
// below and will not suppress a diagnostic raised on itself.
func parseSuppressions(lines []string) []suppression {
	var sups []suppression
	for i, line := range lines {
		m := ignoreDirectiveRe.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}

		target := i + 2 // own-line directive: the line below
		if strings.TrimSpace(line[:m[0]]) != "" {
			target = i + 1 // trailing directive: this same line
		}

		spec := line[m[2]:m[3]]
		if reason := strings.Index(spec, " -- "); reason >= 0 {
			spec = spec[:reason]
		}
		var ids []string
		for id := range strings.SplitSeq(spec, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}

		sups = append(sups, suppression{dirLine: i + 1, col: m[0] + 1, target: target, ruleIDs: ids})
	}
	return sups
}

// applySuppressions drops diagnostics covered by a `templint:ignore` directive
// and reports the directives that are themselves broken: unknown rule IDs
// (so typos do not silently suppress nothing) and directives that suppressed
// nothing at all (so stale ones get cleaned up).
//
// A directive naming only rules that are known but not enabled is left alone —
// turning a rule "off" in hamr.toml must not turn every existing suppression
// into a warning.
func (l *Linter) applySuppressions(path string, lines []string, diags []Diagnostic) []Diagnostic {
	if len(l.rules) == 0 {
		return diags
	}
	sups := parseSuppressions(lines)
	if len(sups) == 0 {
		return diags
	}

	enabled := make(map[string]bool, len(l.rules))
	for _, r := range l.rules {
		enabled[r.ID()] = true
	}

	kept := make([]Diagnostic, 0, len(diags))
	used := make([]bool, len(sups))
	for _, d := range diags {
		suppressed := false
		for i, s := range sups {
			if s.target != d.Line {
				continue
			}
			if len(s.ruleIDs) == 0 || slices.Contains(s.ruleIDs, d.Rule) {
				suppressed = true
				used[i] = true
			}
		}
		if !suppressed {
			kept = append(kept, d)
		}
	}

	for i, s := range sups {
		anyEnabled := len(s.ruleIDs) == 0
		for _, id := range s.ruleIDs {
			if _, known := rules[id]; !known {
				kept = append(kept, Diagnostic{
					File: path, Line: s.dirLine, Col: s.col,
					Rule: "unknown-rule", Severity: Error,
					Message: fmt.Sprintf("unknown rule %q in templint:ignore directive (known: %v)", id, AllRuleIDs()),
				})
				continue
			}
			if enabled[id] {
				anyEnabled = true
			}
		}
		if !used[i] && anyEnabled {
			kept = append(kept, Diagnostic{
				File: path, Line: s.dirLine, Col: s.col,
				Rule: "unused-suppression", Severity: Warning,
				Message: "templint:ignore directive suppresses nothing; remove it",
			})
		}
	}

	return kept
}

// LintDir walks a directory tree and lints all .templ files.
func (l *Linter) LintDir(dir string) ([]Diagnostic, error) {
	var all []Diagnostic

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".templ") {
			return nil
		}

		diags, err := l.LintFile(path)
		if err != nil {
			return err
		}
		all = append(all, diags...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].File != all[j].File {
			return all[i].File < all[j].File
		}
		if all[i].Line != all[j].Line {
			return all[i].Line < all[j].Line
		}
		return all[i].Col < all[j].Col
	})

	return all, nil
}

// FilterBySeverity returns only diagnostics at or above the given severity.
func FilterBySeverity(diags []Diagnostic, minSev Severity) []Diagnostic {
	var filtered []Diagnostic
	for _, d := range diags {
		if d.Severity >= minSev {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// HasErrors returns true if any diagnostic has Error severity.
func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}
