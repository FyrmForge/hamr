package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
)

var addSkillCmd = &cobra.Command{
	Use:   "skill <target>",
	Short: "Install an AI agent skill describing the HAMR framework",
	Long: `Install an AI agent skill that teaches the target tool how to use the hamr CLI,
which hamr packages exist, and the project's Go + templ + HTMX + Alpine coding
practices.

Currently supported targets: claude.

By default, the skill is written to ./.claude/skills/hamr/ and the command must
be run from the root of a HAMR project (a directory containing hamr.toml).
Use --global to install to ~/.claude/skills/hamr/ instead, which works from any
directory.

Examples:
  hamr add skill claude
  hamr add skill claude --global
  hamr add skill claude --force`,
	Args: cobra.ExactArgs(1),
	RunE: runAddSkill,
}

func init() {
	addSkillCmd.Flags().Bool("global", false, "install to ~/.claude/skills/ instead of ./.claude/skills/")
	addSkillCmd.Flags().Bool("force", false, "overwrite an existing skill directory")
	addCmd.AddCommand(addSkillCmd)
}

func runAddSkill(cmd *cobra.Command, args []string) error {
	target := strings.ToLower(args[0])
	if !slices.Contains(generator.SupportedSkillTargets, target) {
		return fmt.Errorf("unsupported skill target %q (supported: %s)",
			target, strings.Join(generator.SupportedSkillTargets, ", "))
	}

	global, _ := cmd.Flags().GetBool("global")
	force, _ := cmd.Flags().GetBool("force")

	destDir, err := resolveSkillDest(target, global)
	if err != nil {
		return err
	}

	if err := generator.InstallSkill(target, destDir, force); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s skill at %s\n", target, destDir)
	return nil
}

// resolveSkillDest returns the destination directory for the given target.
// Project installs (global=false) require a hamr.toml in CWD.
func resolveSkillDest(target string, global bool) (string, error) {
	rel := filepath.Join(skillToolDir(target), "skills", "hamr")

	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, rel), nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "hamr.toml")); err != nil {
		return "", fmt.Errorf("no hamr.toml in current directory — run from a HAMR project root, or use --global")
	}
	return filepath.Join(cwd, rel), nil
}

// skillToolDir maps a target name to the directory that tool reads skills from.
func skillToolDir(target string) string {
	switch target {
	case "claude":
		return ".claude"
	default:
		return "." + target
	}
}
