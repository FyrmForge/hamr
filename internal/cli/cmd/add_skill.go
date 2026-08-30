package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/FyrmForge/hamr/internal/scaffold"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var addSkillCmd = &cobra.Command{
	Use:   "skill [target]",
	Short: "Install AI agent skills for working on a HAMR project",
	Long: `Install AI agent skills: the hamr framework skill (CLI, packages, Go + templ +
HTMX practices, plus Alpine.js guidance when the project opted in), a QA
test-and-fix loop driven over Playwright + hamr MCP, and a PR-publish workflow
with gist-hosted screenshots.

With no arguments, an interactive picker asks which agents and which skills to
install (an explicit --skills seeds the selection). With a target argument it
runs non-interactively and installs the skills named by --skills (all of them
by default).

Supported targets: claude (installs to .claude/skills/), codex and opencode
(both install to the shared .agents/skills/, so picking both writes once).
Available skills: ` + strings.Join(generator.SkillNames, ", ") + `.

By default, skills are written into the project and the command must be run
from the root of a HAMR project (a directory containing hamr.toml). Use
--global to install under the home directory (~/.claude/skills/ or
~/.agents/skills/) instead, which works from any directory.

Examples:
  hamr add skill                          # interactive picker
  hamr add skill claude                   # all skills, non-interactive
  hamr add skill codex --skills qa-loop   # one skill → .agents/skills/
  hamr add skill claude --global --force`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAddSkill,
}

func init() {
	addSkillCmd.Flags().Bool("global", false, "install under the home directory instead of the project")
	addSkillCmd.Flags().Bool("force", false, "overwrite existing skill directories")
	addSkillCmd.Flags().StringSlice("skills", generator.SkillNames,
		"which skills to install (comma-separated): "+strings.Join(generator.SkillNames, ", "))
	addCmd.AddCommand(addSkillCmd)
}

func runAddSkill(cmd *cobra.Command, args []string) error {
	global, _ := cmd.Flags().GetBool("global")
	force, _ := cmd.Flags().GetBool("force")

	var targets, skills []string
	if len(args) == 0 {
		// Fail before the form, not after the user has filled it in.
		if _, err := resolveSkillBase("claude", global); err != nil {
			return err
		}
		// An explicit --skills seeds the picker; otherwise installed skills
		// (or, on a fresh install, all of them) start ticked.
		var preselect []string
		if cmd.Flags().Changed("skills") {
			preselect, _ = cmd.Flags().GetStringSlice("skills")
		}
		targets = detectedSkillTargets()
		skills = preselect
		if err := newAddSkillForm(global, &targets, &skills).Run(); err != nil {
			if strings.Contains(err.Error(), "could not open a new TTY") {
				return fmt.Errorf("no interactive terminal; use `hamr add skill claude [--skills ...]` instead")
			}
			return err
		}
		// The picker is an explicit choice, so overwrite installed skills
		// without demanding --force.
		force = true
	} else {
		targets = []string{strings.ToLower(args[0])}
		skills, _ = cmd.Flags().GetStringSlice("skills")
	}

	for _, target := range targets {
		if !slices.Contains(generator.SupportedSkillTargets, target) {
			return fmt.Errorf("unsupported skill target %q (supported: %s)",
				target, strings.Join(generator.SupportedSkillTargets, ", "))
		}
	}
	for _, skill := range skills {
		if !slices.Contains(generator.SkillNames, skill) {
			return fmt.Errorf("unknown skill %q (available: %s)",
				skill, strings.Join(generator.SkillNames, ", "))
		}
	}
	if len(targets) == 0 || len(skills) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Nothing selected — no skills installed.")
		return nil
	}

	data := loadSkillData(global)
	seen := map[string]bool{} // codex and opencode share .agents/skills — write once
	for _, target := range targets {
		base, err := resolveSkillBase(target, global)
		if err != nil {
			return err
		}
		if seen[base] {
			continue
		}
		seen[base] = true
		for _, skill := range skills {
			dest := filepath.Join(base, generator.SkillDirName(skill))
			if err := generator.InstallSkill(skill, dest, force, data); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s skill %s at %s\n", target, skill, dest)
		}
	}
	return nil
}

// detectedSkillTargets pre-ticks the picker with the agents that look present
// on this machine/project (same detection the MCP installers use).
func detectedSkillTargets() []string {
	root, err := os.Getwd()
	if err != nil {
		return nil
	}
	all := installers()
	var out []string
	for _, name := range installOrder {
		if all[name].detect(root) {
			out = append(out, name)
		}
	}
	return out
}

// newAddSkillForm builds the two-level picker: agents first, then skills.
// When skills is empty it is seeded with what is already installed (first
// target that has any), or with every skill on a fresh install.
// Split from runAddSkill so tests can drive it with scripted input via huh's
// WithInput/WithOutput instead of needing a TTY.
func newAddSkillForm(global bool, targets, skills *[]string) *huh.Form {
	if len(*skills) == 0 {
		for _, t := range generator.SupportedSkillTargets {
			if inst := installedSkills(t, global); len(inst) > 0 {
				*skills = inst
				break
			}
		}
	}
	if len(*skills) == 0 {
		*skills = slices.Clone(generator.SkillNames)
	}

	return huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Agents").
				Description("Which AI agents to install skills for.").
				Options(skillTargetOptions()...).
				Value(targets),
			huh.NewMultiSelect[string]().
				Title("Skills").
				Description(skillPickerHelp).
				Options(skillNameOptions()...).
				Value(skills),
		),
	)
}

const skillPickerHelp = "hamr — framework skill (CLI, packages, templ/HTMX practices)\n" +
	"qa-loop — QA test-and-fix loop over Playwright + hamr MCP\n" +
	"pr-publish — PR with structured body + gist-hosted screenshots\n" +
	"Selected skills overwrite any existing install."

func skillTargetOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(generator.SupportedSkillTargets))
	for _, t := range generator.SupportedSkillTargets {
		label := t
		if t != "claude" {
			label += " (.agents/skills)"
		}
		opts = append(opts, huh.NewOption(label, t))
	}
	return opts
}

func skillNameOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(generator.SkillNames))
	for _, s := range generator.SkillNames {
		opts = append(opts, huh.NewOption(s, s))
	}
	return opts
}

// installedSkills reports which skills already exist on disk for target.
func installedSkills(target string, global bool) []string {
	base, err := resolveSkillBase(target, global)
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range generator.SkillNames {
		if _, err := os.Stat(filepath.Join(base, generator.SkillDirName(s))); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// resolveSkillBase returns the skills directory for the given target
// (e.g. ./.claude/skills). Project installs (global=false) require a hamr.toml
// in CWD.
func resolveSkillBase(target string, global bool) (string, error) {
	rel := filepath.Join(skillToolDir(target), "skills")

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

// loadSkillData builds the render context for skill templates from the current
// project. Global installs default to the scaffold baseline (Alpine off).
// For project installs, Alpine is considered enabled if either hamr.toml's
// [options].alpine is true, OR <static dir>/js/alpine.min.js exists on disk — the
// disk check covers projects scaffolded before the alpine key was tracked and
// projects that opted in via `hamr vendor alpine` post-scaffold.
func loadSkillData(global bool) generator.SkillData {
	if global {
		return generator.SkillData{}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return generator.SkillData{}
	}

	alpine := false
	if meta, err := scaffold.LoadMetadata(filepath.Join(cwd, "hamr.toml")); err == nil {
		alpine = meta.Options.Alpine
	}
	if !alpine {
		if _, err := os.Stat(filepath.Join(cwd, staticDirFromConfig("static"), "js", "alpine.min.js")); err == nil {
			alpine = true
		}
	}

	return generator.SkillData{IncludeAlpine: alpine}
}

// skillToolDir maps a target name to the directory that tool reads skills
// from. codex and opencode both read the cross-tool standard .agents/skills/
// (opencode reads .claude/skills/ too, but installing there for a non-claude
// target would surprise).
func skillToolDir(target string) string {
	if target == "claude" {
		return ".claude"
	}
	return ".agents"
}
