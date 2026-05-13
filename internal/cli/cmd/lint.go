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

	// --rule flag overrides config entirely: enable only the listed rules at
	// their recommended default severity.
	if len(ruleFilter) > 0 {
		known := make(map[string]bool)
		for _, id := range templint.AllRuleIDs() {
			known[id] = true
		}
		rules := make(map[string]templint.Severity, len(ruleFilter))
		for _, r := range ruleFilter {
			if !known[r] {
				return fmt.Errorf("unknown rule: %q", r)
			}
			rules[r] = templint.DefaultSeverity(r)
		}
		cfg = &templint.Config{Rules: rules}
	}

	linter := templint.New(cfg)
	if linter.RuleCount() == 0 {
		fmt.Fprintf(os.Stderr,
			"templint: no rules enabled (add a [lint.templ] block to %s or pass --rule); nothing to check\n",
			configPath)
	}
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
