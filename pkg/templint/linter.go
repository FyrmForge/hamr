package templint

import (
	"os"
	"path/filepath"
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

	sort.Slice(diags, func(i, j int) bool {
		if diags[i].Line != diags[j].Line {
			return diags[i].Line < diags[j].Line
		}
		return diags[i].Col < diags[j].Col
	})

	return diags, nil
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
