package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/FyrmForge/hamr/internal/scaffold"
	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Show scaffold changes between project version and current HAMR",
	Long: `Compare the project's scaffold baseline version to the current HAMR version
and produce a report of what has changed.

The report includes categorized changes with relevance annotations based on
the project's scaffold options.

Examples:
  hamr ai upgrade
  hamr ai upgrade --json
  hamr ai upgrade --category structural --relevant-only
  hamr ai upgrade --from 0.1.0
  hamr ai upgrade --applied`,
	Args: cobra.NoArgs,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().Bool("json", false, "output as JSON")
	upgradeCmd.Flags().String("category", "", "filter to a specific category")
	upgradeCmd.Flags().String("from", "", "override the base version to diff from")
	upgradeCmd.Flags().Bool("relevant-only", false, "only show changes relevant to project options")
	upgradeCmd.Flags().Bool("applied", false, "update project baseline to current HAMR version")
}

func runUpgrade(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	category, _ := cmd.Flags().GetString("category")
	fromVersion, _ := cmd.Flags().GetString("from")
	relevantOnly, _ := cmd.Flags().GetBool("relevant-only")
	applied, _ := cmd.Flags().GetBool("applied")

	if version == "dev" {
		return fmt.Errorf("cannot determine current HAMR version (running dev build)")
	}

	// --applied: bump the baseline version forward.
	// Runs before metadata checks so it works on legacy projects without [hamr].
	if applied {
		if err := scaffold.UpdateVersion("hamr.toml", version); err != nil {
			return fmt.Errorf("update version: %w", err)
		}
		return writeUpgradeLine(cmd.OutOrStdout(), "updated [hamr] version to %s\n", version)
	}

	meta, err := scaffold.LoadMetadata("hamr.toml")
	if err != nil {
		return fmt.Errorf("load hamr.toml: %w", err)
	}

	if !meta.HasHamrSection() && fromVersion == "" {
		return fmt.Errorf("no [hamr] section in hamr.toml; use --from to specify a base version")
	}

	filters := scaffold.ReportFilters{
		Category:     scaffold.Category(category),
		FromVersion:  fromVersion,
		RelevantOnly: relevantOnly,
	}

	report, err := scaffold.BuildReport(meta, version, scaffold.Changes(), filters)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	return writeHumanReport(cmd.OutOrStdout(), report)
}

func writeHumanReport(w io.Writer, report *scaffold.UpgradeReport) error {
	if _, err := fmt.Fprintf(w, "scaffold upgrade report\n"); err != nil {
		return err
	}

	projectLine := fmt.Sprintf("  project: v%s", report.Project.BaseVersion)
	if report.Project.ScaffoldedAt != "" {
		projectLine += fmt.Sprintf(" (%s)", report.Project.ScaffoldedAt)
	}
	if _, err := fmt.Fprintln(w, projectLine); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  current: v%s\n", report.Project.CurrentVersion); err != nil {
		return err
	}

	if len(report.Changes) == 0 {
		_, err := fmt.Fprintf(w, "\nno scaffold changes between v%s and v%s\n",
			report.Project.BaseVersion, report.Project.CurrentVersion)
		return err
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	for i, c := range report.Changes {
		relevance := ""
		if !c.Relevant {
			relevance = " (not relevant)"
		}
		if _, err := fmt.Fprintf(w, "%d. [%s] %s (since v%s)%s\n",
			i+1, c.Category, c.Title, c.Since, relevance); err != nil {
			return err
		}
		if c.Summary != "" {
			if _, err := fmt.Fprintf(w, "   %s\n", c.Summary); err != nil {
				return err
			}
		}
		if !c.Relevant && c.RelevanceReason != "" {
			if _, err := fmt.Fprintf(w, "   reason: %s\n", c.RelevanceReason); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeUpgradeLine(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}
