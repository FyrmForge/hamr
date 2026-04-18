package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAddSkillTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "skill",
		Args: cobra.ExactArgs(1),
		RunE: runAddSkill,
	}
	cmd.Flags().Bool("global", false, "")
	cmd.Flags().Bool("force", false, "")
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
