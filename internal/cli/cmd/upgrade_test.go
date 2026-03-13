package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/internal/scaffold"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUpgradeTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		RunE: runUpgrade,
	}
	cmd.Flags().Bool("json", false, "output as JSON")
	cmd.Flags().String("from", "", "from version")
	cmd.Flags().Bool("applied", false, "mark as applied")
	cmd.Flags().String("dir", "", "report dir")
	return cmd
}

// mockGitDiff returns a canned DiffReport for testing.
func mockGitDiff(_ context.Context, _, base, current string) (*scaffold.DiffReport, error) {
	if base == current {
		return &scaffold.DiffReport{
			Project: scaffold.DiffProjectInfo{
				BaseVersion:    base,
				CurrentVersion: current,
			},
		}, nil
	}
	return &scaffold.DiffReport{
		Project: scaffold.DiffProjectInfo{
			BaseVersion:    base,
			CurrentVersion: current,
		},
		Diff:     "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new",
		DiffStat: " file.txt | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)",
	}, nil
}

func withMockGitDiff(t *testing.T) {
	t.Helper()
	old := gitDiffFunc
	gitDiffFunc = mockGitDiff
	t.Cleanup(func() { gitDiffFunc = old })
}

func TestUpgradeDevVersion(t *testing.T) {
	old := version
	version = "dev"
	defer func() { version = old }()

	cmd := newUpgradeTestCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dev build")
}

func TestUpgradeMissingHamrToml(t *testing.T) {
	old := version
	version = "0.5.0"
	defer func() { version = old }()

	// Change to a temp dir without hamr.toml.
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(t.TempDir())

	cmd := newUpgradeTestCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hamr.toml")
}

func TestUpgradeMissingHamrSection(t *testing.T) {
	old := version
	version = "0.5.0"
	defer func() { version = old }()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(`[proxy]
listen = ":3000"
`), 0o644))

	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(dir)

	cmd := newUpgradeTestCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from")
}

func TestUpgradeMissingHamrSectionWithFrom(t *testing.T) {
	withMockGitDiff(t)

	old := version
	version = "0.5.0"
	defer func() { version = old }()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(`[proxy]
listen = ":3000"
`), 0o644))

	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(dir)

	cmd := newUpgradeTestCmd()
	require.NoError(t, cmd.Flags().Set("from", "0.1.0"))

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := cmd.RunE(cmd, nil)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "scaffold upgrade report")
	assert.Contains(t, output, "v0.1.0")
	assert.Contains(t, output, "v0.5.0")
	assert.Contains(t, output, "--- diff ---")
	assert.Contains(t, output, "report saved to")
}

func TestUpgradeHumanOutput(t *testing.T) {
	withMockGitDiff(t)

	old := version
	version = "0.5.0"
	defer func() { version = old }()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(`[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"
auth = "session"
`), 0o644))

	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(dir)

	cmd := newUpgradeTestCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.RunE(cmd, nil)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "scaffold upgrade report")
	assert.Contains(t, output, "v0.3.2")
	assert.Contains(t, output, "v0.5.0")
	assert.Contains(t, output, "--- summary ---")
	assert.Contains(t, output, "--- diff ---")
	assert.Contains(t, output, "report saved to")
}

func TestUpgradeHumanOutputNoDiff(t *testing.T) {
	withMockGitDiff(t)

	old := version
	version = "0.3.2"
	defer func() { version = old }()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(`[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"
`), 0o644))

	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(dir)

	cmd := newUpgradeTestCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.RunE(cmd, nil)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "no changes between")
}

func TestUpgradeJSONOutput(t *testing.T) {
	withMockGitDiff(t)

	old := version
	version = "0.5.0"
	defer func() { version = old }()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(`[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"
`), 0o644))

	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(dir)

	cmd := newUpgradeTestCmd()
	require.NoError(t, cmd.Flags().Set("json", "true"))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.RunE(cmd, nil)
	require.NoError(t, err)

	// Stdout should be pure JSON (no "report saved to" mixed in).
	var report scaffold.DiffReport
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	assert.Equal(t, "0.3.2", report.Project.BaseVersion)
	assert.Equal(t, "0.5.0", report.Project.CurrentVersion)
	assert.NotEmpty(t, report.Diff)

	// "report saved to" goes to stderr in JSON mode.
	assert.Contains(t, stderr.String(), "report saved to")
}

func TestUpgradeApplied(t *testing.T) {
	old := version
	version = "0.5.0"
	defer func() { version = old }()

	dir := t.TempDir()
	tomlContent := `[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(tomlContent), 0o644))

	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(dir)

	cmd := newUpgradeTestCmd()
	require.NoError(t, cmd.Flags().Set("applied", "true"))
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.RunE(cmd, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "0.5.0")

	// Verify hamr.toml was updated.
	updated, err := os.ReadFile(filepath.Join(dir, "hamr.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(updated), `version = "0.5.0"`)
	assert.Contains(t, string(updated), `scaffolded_at = "2026-03-11"`)
}

func TestUpgradeAppliedLegacyProject(t *testing.T) {
	old := version
	version = "0.5.0"
	defer func() { version = old }()

	dir := t.TempDir()
	// Legacy project: no [hamr] section.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(`[proxy]
listen = ":3000"
`), 0o644))

	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }() //nolint:errcheck
	_ = os.Chdir(dir)

	cmd := newUpgradeTestCmd()
	require.NoError(t, cmd.Flags().Set("applied", "true"))
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := cmd.RunE(cmd, nil)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "0.5.0")

	// Verify [hamr] section was inserted.
	updated, err := os.ReadFile(filepath.Join(dir, "hamr.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(updated), `[hamr]`)
	assert.Contains(t, string(updated), `version = "0.5.0"`)
	assert.Contains(t, string(updated), `listen = ":3000"`)
}

func TestNormalizeHamrVersion(t *testing.T) {
	t.Run("released versions pass through", func(t *testing.T) {
		assert.Equal(t, "0.3.2", normalizeHamrVersion("0.3.2"))
		assert.Equal(t, "1.2.3", normalizeHamrVersion("v1.2.3"))
		assert.Equal(t, "0.5.0-dev", normalizeHamrVersion("0.5.0-dev"))
	})

	t.Run("dev resolves to tag-dev or 0.0.0-dev", func(t *testing.T) {
		result := normalizeHamrVersion("dev")
		assert.True(t, strings.HasSuffix(result, "-dev"), "expected -dev suffix, got %q", result)
	})

	t.Run("empty resolves to tag-dev or 0.0.0-dev", func(t *testing.T) {
		result := normalizeHamrVersion("")
		assert.True(t, strings.HasSuffix(result, "-dev"), "expected -dev suffix, got %q", result)
	})
}

func TestLatestGitTag(t *testing.T) {
	// This test runs inside the hamr repo which has tags, so it should return something.
	tag := latestGitTag()
	if tag == "" {
		t.Skip("no git tags available")
	}
	// Should be a valid semver base (digits and dots only, no "v" prefix).
	assert.NotContains(t, tag, "v")
	parts := strings.Split(tag, ".")
	assert.Len(t, parts, 3, "expected major.minor.patch, got %q", tag)
}
