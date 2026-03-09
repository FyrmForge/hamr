package templint

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// RuleConfig holds per-rule configuration.
type RuleConfig struct {
	Enabled  *bool  `toml:"enabled"`
	Severity string `toml:"severity"`
}

// Config holds the linter configuration.
type Config struct {
	Rules map[string]RuleConfig `toml:"rules"`
}

// tomlFile mirrors the hamr.toml structure so we can decode the
// [lint.templ] section directly.
type tomlFile struct {
	Lint struct {
		Templ Config `toml:"templ"`
	} `toml:"lint"`
}

// LoadConfig reads a hamr.toml file and returns the parsed [lint.templ] Config.
// Returns nil if the file does not exist (use defaults).
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
	if len(f.Lint.Templ.Rules) == 0 {
		return nil, nil
	}
	return &f.Lint.Templ, nil
}

// IsEnabled returns whether a rule is enabled in this config.
// Returns true if the config is nil or the rule is not configured.
func (c *Config) IsEnabled(ruleID string) bool {
	if c == nil || c.Rules == nil {
		return true
	}
	rc, ok := c.Rules[ruleID]
	if !ok {
		return true
	}
	if rc.Enabled == nil {
		return true
	}
	return *rc.Enabled
}

// GetSeverity returns the configured severity for a rule, falling back to
// the provided default if not configured.
func (c *Config) GetSeverity(ruleID string, defaultSev Severity) Severity {
	if c == nil || c.Rules == nil {
		return defaultSev
	}
	rc, ok := c.Rules[ruleID]
	if !ok || rc.Severity == "" {
		return defaultSev
	}
	sev, err := ParseSeverity(rc.Severity)
	if err != nil {
		return defaultSev
	}
	return sev
}
