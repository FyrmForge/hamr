package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAddSkillTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "skill",
		Args: cobra.MaximumNArgs(1),
		RunE: runAddSkill,
	}
	cmd.Flags().Bool("global", false, "")
	cmd.Flags().Bool("force", false, "")
	cmd.Flags().StringSlice("skills", generator.SkillNames, "")
	return cmd
}

// chdir cds to dir for the duration of the test and restores afterwards.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// writeHamrToml marks dir as a HAMR project root by creating an empty hamr.toml.
func writeHamrToml(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte("[hamr]\nversion = \"0.0.0\"\n"), 0o644))
}

func TestAddSkill_ProjectInstall_WritesExpectedFiles(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"claude"})
	require.NoError(t, cmd.Execute())

	for _, rel := range []string{
		".claude/skills/hamr/SKILL.md",
		".claude/skills/hamr/references/cli.md",
		".claude/skills/hamr/references/packages.md",
		".claude/skills/hamr/references/practices.md",
		".claude/skills/hamr-qa-loop/SKILL.md",
		".claude/skills/hamr-pr-publish/SKILL.md",
	} {
		_, err := os.Stat(filepath.Join(dir, rel))
		assert.NoErrorf(t, err, "expected %s to exist", rel)
	}

	// Stdout should report the install location.
	assert.Contains(t, buf.String(), filepath.Join(dir, ".claude/skills/hamr"))
}

func TestAddSkill_ProjectInstall_RequiresHamrToml(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hamr.toml")
}

func TestAddSkill_ExistingDir_ErrorsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude", "skills", "hamr"), 0o755))

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
}

func TestAddSkill_ForceOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)
	stale := filepath.Join(dir, ".claude", "skills", "hamr", "stale.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o644))

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude", "--force"})
	cmd.Flags().Set("force", "true") //nolint:errcheck
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(stale)
	assert.True(t, os.IsNotExist(err), "stale file should be removed by --force")
	_, err = os.Stat(filepath.Join(dir, ".claude/skills/hamr/SKILL.md"))
	assert.NoError(t, err)
}

func TestAddSkill_GlobalWritesToHome(t *testing.T) {
	// Redirect HOME so the global path stays under TempDir.
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Run from a non-HAMR cwd to prove --global does not need hamr.toml.
	nonProject := t.TempDir()
	chdir(t, nonProject)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude", "--global"})
	cmd.Flags().Set("global", "true") //nolint:errcheck
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(filepath.Join(home, ".claude/skills/hamr/SKILL.md"))
	assert.NoError(t, err)
}

func TestAddSkill_RendersWithAlpineOn(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"),
		[]byte("[hamr]\nversion = \"0.0.0\"\n\n[options]\nalpine = true\n"), 0o644))
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude"})
	require.NoError(t, cmd.Execute())

	practices, err := os.ReadFile(filepath.Join(dir, ".claude/skills/hamr/references/practices.md"))
	require.NoError(t, err)
	assert.Contains(t, string(practices), "## Alpine.js", "practices.md should include the Alpine section when project opted in")
	assert.Contains(t, string(practices), "Alpine components")

	skill, err := os.ReadFile(filepath.Join(dir, ".claude/skills/hamr/SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(skill), "**Alpine.js**")
}

func TestAddSkill_RendersWithAlpineOff(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude"})
	require.NoError(t, cmd.Execute())

	practices, err := os.ReadFile(filepath.Join(dir, ".claude/skills/hamr/references/practices.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(practices), "## Alpine.js", "practices.md should omit the Alpine section when project opted out")

	skill, err := os.ReadFile(filepath.Join(dir, ".claude/skills/hamr/SKILL.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(skill), "**Alpine.js**")

	cli, err := os.ReadFile(filepath.Join(dir, ".claude/skills/hamr/references/cli.md"))
	require.NoError(t, err)
	assert.Contains(t, string(cli), "scaffolded without Alpine")
}

func TestAddSkill_AlpineDetectedFromDisk(t *testing.T) {
	// Simulates a project scaffolded before the alpine option was tracked:
	// hamr.toml has no alpine key, but static/js/alpine.min.js exists on disk.
	dir := t.TempDir()
	writeHamrToml(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "static", "js"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "static", "js", "alpine.min.js"), []byte("/* alpine */"), 0o644))
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude"})
	require.NoError(t, cmd.Execute())

	practices, err := os.ReadFile(filepath.Join(dir, ".claude/skills/hamr/references/practices.md"))
	require.NoError(t, err)
	assert.Contains(t, string(practices), "## Alpine.js", "Alpine section should render when alpine.min.js is present on disk")
}

func TestAddSkill_SkillsFlagSelectsSubset(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude"})
	cmd.Flags().Set("skills", "qa-loop") //nolint:errcheck
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(filepath.Join(dir, ".claude/skills/hamr-qa-loop/SKILL.md"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, ".claude/skills/hamr"))
	assert.True(t, os.IsNotExist(err), "unselected skills should not be installed")
	_, err = os.Stat(filepath.Join(dir, ".claude/skills/hamr-pr-publish"))
	assert.True(t, os.IsNotExist(err), "unselected skills should not be installed")
}

func TestAddSkill_UnknownSkillErrors(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"claude"})
	cmd.Flags().Set("skills", "nope") //nolint:errcheck
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown skill")
}

func TestInstalledSkills_ReportsExisting(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude", "skills", "hamr-qa-loop"), 0o755))

	assert.Equal(t, []string{"qa-loop"}, installedSkills("claude", false))
}

// Drives the real picker headlessly, like TestSetupForm_DrivenHeadlessly.
// Keys: in the agents multiselect toggle the pre-ticked claude off, then
// accept the rest; the skills field (like setup's undriven last group) keeps
// its seeded default through the trailing EOF.
func TestAddSkillForm_DrivenHeadlessly(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	targets := []string{"claude"}
	var skills []string // empty → seeded inside newAddSkillForm (fresh install: all)
	form := newAddSkillForm(false, &targets, &skills).
		WithInput(strings.NewReader("x\r" + strings.Repeat("\r", 5))).
		WithOutput(io.Discard)

	require.NoError(t, form.Run())
	assert.Empty(t, targets, "x untoggled the pre-ticked claude")
	assert.Equal(t, []string{"hamr", "qa-loop", "pr-publish"}, skills, "undriven skills field keeps the seeded default")
}

func TestAddSkillForm_SeedsFromInstalled(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude", "skills", "hamr-pr-publish"), 0o755))

	targets := []string{"claude"}
	var skills []string
	_ = newAddSkillForm(false, &targets, &skills)
	assert.Equal(t, []string{"pr-publish"}, skills, "installed skills pre-ticked")

	skills = []string{"qa-loop"} // explicit --skills wins over installed
	_ = newAddSkillForm(false, &targets, &skills)
	assert.Equal(t, []string{"qa-loop"}, skills)
}

func TestAddSkill_Interactive_FailsFastWithoutHamrToml(t *testing.T) {
	dir := t.TempDir() // no hamr.toml
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hamr.toml", "should fail before showing the form")
}

func TestAddSkill_CodexInstallsToAgentsDir(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"codex"})
	require.NoError(t, cmd.Execute())

	for _, rel := range []string{
		".agents/skills/hamr/SKILL.md",
		".agents/skills/hamr-qa-loop/SKILL.md",
		".agents/skills/hamr-pr-publish/SKILL.md",
	} {
		_, err := os.Stat(filepath.Join(dir, rel))
		assert.NoErrorf(t, err, "expected %s to exist", rel)
	}
	_, err := os.Stat(filepath.Join(dir, ".claude"))
	assert.True(t, os.IsNotExist(err), "codex install should not touch .claude")
}

func TestAddSkill_OpencodeSharesAgentsDir(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"opencode"})
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(filepath.Join(dir, ".agents/skills/hamr/SKILL.md"))
	assert.NoError(t, err)

	// A second install for codex without --force hits the same dir and errors —
	// proof both targets resolve to one location.
	cmd = newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"codex"})
	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
}

func TestAddSkill_UnsupportedTargetErrors(t *testing.T) {
	dir := t.TempDir()
	writeHamrToml(t, dir)
	chdir(t, dir)

	cmd := newAddSkillTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"copilot"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported skill target")
}
