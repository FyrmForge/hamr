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

// New creates a Linter with rules configured by the given Config.
// Pass nil for default configuration (all rules enabled).
func New(cfg *Config) *Linter {
	var rules []Rule

	// Control flow rules (default: error)
	if cfg.IsEnabled("inline-if") {
		rules = append(rules, &inlineIfRule{severity: cfg.GetSeverity("inline-if", Error)})
	}
	if cfg.IsEnabled("inline-for") {
		rules = append(rules, &inlineForRule{severity: cfg.GetSeverity("inline-for", Error)})
	}
	if cfg.IsEnabled("inline-switch") {
		rules = append(rules, &inlineSwitchRule{severity: cfg.GetSeverity("inline-switch", Error)})
	}

	// Accessibility rules (default: warning)
	if cfg.IsEnabled("img-alt") {
		rules = append(rules, &imgAltRule{severity: cfg.GetSeverity("img-alt", Warning)})
	}
	if cfg.IsEnabled("no-href") {
		rules = append(rules, &noHrefRule{severity: cfg.GetSeverity("no-href", Warning)})
	}

	// Style rules (default: warning)
	if cfg.IsEnabled("inline-style") {
		rules = append(rules, &inlineStyleRule{severity: cfg.GetSeverity("inline-style", Warning)})
	}
	if cfg.IsEnabled("empty-class") {
		rules = append(rules, &emptyClassRule{severity: cfg.GetSeverity("empty-class", Warning)})
	}
	if cfg.IsEnabled("js-href") {
		rules = append(rules, &jsHrefRule{severity: cfg.GetSeverity("js-href", Warning)})
	}

	return &Linter{rules: rules}
}

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
