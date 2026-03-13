package scaffold

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestRepo creates a local git repo with two tagged commits for testing.
// Returns the repo path. The repo has:
//   - v0.1.0: initial commit with file.txt containing "hello"
//   - v0.2.0: second commit with file.txt containing "hello world"
func setupTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	// First commit + tag.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644))
	run("add", ".")
	run("commit", "-m", "initial")
	run("tag", "v0.1.0")

	// Second commit + tag.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello world"), 0o644))
	run("add", ".")
	run("commit", "-m", "update")
	run("tag", "v0.2.0")

	return dir
}

func TestGitDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("produces diff between tags", func(t *testing.T) {
		repo := setupTestRepo(t)
		report, err := GitDiff(context.Background(), repo, "0.1.0", "0.2.0")
		require.NoError(t, err)

		assert.Equal(t, "0.1.0", report.Project.BaseVersion)
		assert.Equal(t, "0.2.0", report.Project.CurrentVersion)
		assert.Contains(t, report.Diff, "hello world")
		assert.Contains(t, report.DiffStat, "file.txt")
	})

	t.Run("no diff when versions match", func(t *testing.T) {
		repo := setupTestRepo(t)
		report, err := GitDiff(context.Background(), repo, "0.1.0", "0.1.0")
		require.NoError(t, err)

		assert.Empty(t, report.Diff)
		assert.Empty(t, report.DiffStat)
	})

	t.Run("missing base tag", func(t *testing.T) {
		repo := setupTestRepo(t)
		_, err := GitDiff(context.Background(), repo, "9.9.9", "0.2.0")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "base version tag")
	})

	t.Run("missing current tag", func(t *testing.T) {
		repo := setupTestRepo(t)
		_, err := GitDiff(context.Background(), repo, "0.1.0", "9.9.9")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "current version tag")
	})

	t.Run("strips v prefix from versions", func(t *testing.T) {
		repo := setupTestRepo(t)
		report, err := GitDiff(context.Background(), repo, "v0.1.0", "v0.2.0")
		require.NoError(t, err)

		assert.Equal(t, "0.1.0", report.Project.BaseVersion)
		assert.Equal(t, "0.2.0", report.Project.CurrentVersion)
		assert.Contains(t, report.Diff, "hello world")
	})

	t.Run("context cancellation", func(t *testing.T) {
		repo := setupTestRepo(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately
		_, err := GitDiff(ctx, repo, "0.1.0", "0.2.0")
		require.Error(t, err)
	})
}
