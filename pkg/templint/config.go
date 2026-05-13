package templint

import (
	"fmt"
	"os"
	"sort"

	"github.com/BurntSushi/toml"
)

// Config holds the linter configuration. Rules maps a rule ID to the
// severity it should report at; rules not present in the map are disabled.
type Config struct {
	Rules map[string]Severity
}

type tomlFile struct {
	Lint struct {
		Templ map[string]string `toml:"templ"`
	} `toml:"lint"`
}

// LoadConfig reads a hamr.toml file and returns the parsed [lint.templ] Config.
// Returns nil if the file does not exist or the section is empty.
//
// Each entry is a rule ID mapped to one of "warning", "error", or "off".
// An entry of "off" is equivalent to omitting the rule. Unknown rule IDs and
// invalid severities are returned as errors.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var f tomlFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(f.Lint.Templ) == 0 {
		return nil, nil
	}

	cfg := &Config{Rules: make(map[string]Severity)}
	for id, sevStr := range f.Lint.Templ {
		if _, ok := rules[id]; !ok {
			return nil, fmt.Errorf("unknown rule %q in [lint.templ] (known: %v)", id, AllRuleIDs())
		}
		if sevStr == "off" {
			continue
		}
		sev, err := ParseSeverity(sevStr)
		if err != nil {
			return nil, fmt.Errorf("rule %q in [lint.templ]: %w", id, err)
		}
		cfg.Rules[id] = sev
	}
	return cfg, nil
}

// AllRuleIDs returns the sorted IDs of every rule registered with the linter.
func AllRuleIDs() []string {
	ids := make([]string, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// DefaultSeverity returns the recommended default severity for a rule, used
// when callers need to enable a rule without an explicit severity (e.g. the
// --rule CLI flag). Returns Warning for unknown rule IDs.
func DefaultSeverity(id string) Severity {
	if def, ok := rules[id]; ok {
		return def.defaultSev
	}
	return Warning
}
