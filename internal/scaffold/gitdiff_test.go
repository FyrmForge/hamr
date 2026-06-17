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
// Files span the dependency surface (in scope) and hamr's internals (out of
// scope) so the path-scoping of the upgrade diff can be asserted. Each marker
// changes between v0.1.0 and v0.2.0.
//
//	in scope:  pkg/, internal/cli/generator/templates/, docs/, llmsdocs/
//	out:       internal/devserver/ (hamr internals), *_test.go, file.txt (root)
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

	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	commit := func(marker string) {
		// In scope: libs, scaffold templates, docs, llms context.
		write("pkg/lib.go", "package pkg\n\nconst V = \"PKG_"+marker+"\"\n")
		write("internal/cli/generator/templates/site.tmpl", "TEMPLATE_"+marker+"\n")
		write("docs/guide/x.md", "DOCS_"+marker+"\n")
		write("llmsdocs/llms.txt", "LLMS_"+marker+"\n")
		// Out of scope: hamr internals, tests, root files.
		write("internal/devserver/tool.go", "package devserver\n\nconst T = \"INTERNAL_"+marker+"\"\n")
		write("pkg/lib_test.go", "package pkg\n\n// TEST_"+marker+"\n")
		write("file.txt", "ROOT_"+marker+"\n")
		run("add", ".")
		run("commit", "-m", "commit "+marker)
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	commit("V1")
	run("tag", "v0.1.0")
	commit("V2")
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
		assert.Contains(t, report.Diff, "PKG_V2")
		assert.Contains(t, report.DiffStat, "pkg/lib.go")
	})

	t.Run("scopes to the project surface (pkg + templates + docs + llmsdocs)", func(t *testing.T) {
		repo := setupTestRepo(t)
		report, err := GitDiff(context.Background(), repo, "0.1.0", "0.2.0")
		require.NoError(t, err)

		// In scope: libs, scaffold templates, docs, and llms context the project
		// carries and can upgrade.
		assert.Contains(t, report.Diff, "PKG_V2")
		assert.Contains(t, report.Diff, "TEMPLATE_V2")
		assert.Contains(t, report.Diff, "DOCS_V2")
		assert.Contains(t, report.Diff, "LLMS_V2")

		// Out of scope: hamr's own internal tooling, tests, root files.
		assert.NotContains(t, report.Diff, "INTERNAL_V2", "hamr internals must be excluded")
		assert.NotContains(t, report.Diff, "TEST_V2", "tests must be excluded")
		assert.NotContains(t, report.Diff, "ROOT_V2", "unrelated root files must be excluded")
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
		assert.Contains(t, report.Diff, "PKG_V2")
	})

	t.Run("context cancellation", func(t *testing.T) {
		repo := setupTestRepo(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately
		_, err := GitDiff(ctx, repo, "0.1.0", "0.2.0")
		require.Error(t, err)
	})
}
