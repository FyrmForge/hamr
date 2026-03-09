package cmd

import (
	"fmt"
	"os"

	"github.com/FyrmForge/hamr/pkg/templint"
	"github.com/spf13/cobra"
)

var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Run project linters",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var lintTemplCmd = &cobra.Command{
	Use:   "templ",
	Short: "Lint .templ files for common issues",
	Args:  cobra.NoArgs,
	RunE:  runTemplLint,
}

func init() {
	addTemplLintFlags(lintTemplCmd)
	lintCmd.AddCommand(lintTemplCmd)
}

func addTemplLintFlags(cmd *cobra.Command) {
	cmd.Flags().StringSlice("rule", nil, "run only these rules (comma-separated IDs)")
	cmd.Flags().String("config", "hamr.toml", "path to hamr.toml config file")
	cmd.Flags().String("severity", "", "minimum severity to report: warning|error")
}

func runTemplLint(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	ruleFilter, _ := cmd.Flags().GetStringSlice("rule")
	severityStr, _ := cmd.Flags().GetString("severity")

	cfg, err := templint.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// --rule flag: disable all rules not in the filter list.
	if len(ruleFilter) > 0 {
		if cfg == nil {
			cfg = &templint.Config{Rules: make(map[string]templint.RuleConfig)}
		}
		if cfg.Rules == nil {
			cfg.Rules = make(map[string]templint.RuleConfig)
		}
		allowed := make(map[string]bool)
		allRules := []string{
			"inline-if", "inline-for", "inline-switch",
			"img-alt", "no-href",
			"inline-style", "empty-class", "js-href",
		}
		ruleExists := make(map[string]bool, len(allRules))
		for _, id := range allRules {
			ruleExists[id] = true
		}
		for _, r := range ruleFilter {
			if !ruleExists[r] {
				return fmt.Errorf("unknown rule: %q", r)
			}
			allowed[r] = true
		}
		f := false
		t := true
		for _, id := range allRules {
			rc := cfg.Rules[id]
			if allowed[id] {
				rc.Enabled = &t
			} else {
				rc.Enabled = &f
			}
			cfg.Rules[id] = rc
		}
	}

	linter := templint.New(cfg)
	diags, err := linter.LintDir(".")
	if err != nil {
		return err
	}

	// Apply --severity filter.
	if severityStr != "" {
		minSev, err := templint.ParseSeverity(severityStr)
		if err != nil {
			return err
		}
		diags = templint.FilterBySeverity(diags, minSev)
	}

	for _, d := range diags {
		fmt.Println(d)
	}

	if templint.HasErrors(diags) {
		os.Exit(1)
	}
	return nil
}
