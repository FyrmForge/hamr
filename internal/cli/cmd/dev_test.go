package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FyrmForge/hamr/internal/devserver/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withCLIVersion swaps the package-level version + releaseBuild for a test.
func withCLIVersion(t *testing.T, v string, release bool) {
	t.Helper()
	origV, origR := version, releaseBuild
	version = v
	releaseBuild = release
	t.Cleanup(func() { version = origV; releaseBuild = origR })
}

// writeHamrTomlWithVersion writes a minimal hamr.toml with a [hamr] version section.
func writeHamrTomlWithVersion(t *testing.T, dir, v string) string {
	t.Helper()
	path := filepath.Join(dir, "hamr.toml")
	body := "[hamr]\nversion = \"" + v + "\"\nscaffolded_at = \"2025-01-01\"\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestEnsureCLINotBehindScaffoldBlocksWhenCLIOlder(t *testing.T) {
	withCLIVersion(t, "1.0.0", true)
	path := writeHamrTomlWithVersion(t, t.TempDir(), "1.2.0")

	err := ensureCLINotBehindScaffold(path, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v1.2.0")
	assert.Contains(t, err.Error(), "v1.0.0")
	assert.Contains(t, err.Error(), "--skip-version-check")
}

func TestEnsureCLINotBehindScaffoldAllowsWhenCLINewer(t *testing.T) {
	withCLIVersion(t, "2.0.0", true)
	path := writeHamrTomlWithVersion(t, t.TempDir(), "1.5.0")

	require.NoError(t, ensureCLINotBehindScaffold(path, false))
}

func TestEnsureCLINotBehindScaffoldAllowsWhenEqual(t *testing.T) {
	withCLIVersion(t, "1.2.3", true)
	path := writeHamrTomlWithVersion(t, t.TempDir(), "1.2.3")

	require.NoError(t, ensureCLINotBehindScaffold(path, false))
}

func TestEnsureCLINotBehindScaffoldSkippedByFlag(t *testing.T) {
	withCLIVersion(t, "1.0.0", true)
	path := writeHamrTomlWithVersion(t, t.TempDir(), "9.9.9")

	require.NoError(t, ensureCLINotBehindScaffold(path, true))
}

func TestEnsureCLINotBehindScaffoldSkippedOnDevBuild(t *testing.T) {
	withCLIVersion(t, "dev", false)
	path := writeHamrTomlWithVersion(t, t.TempDir(), "9.9.9")

	require.NoError(t, ensureCLINotBehindScaffold(path, false))
}

func TestEnsureCLINotBehindScaffoldNoMetadataFile(t *testing.T) {
	withCLIVersion(t, "1.0.0", true)

	require.NoError(t, ensureCLINotBehindScaffold(filepath.Join(t.TempDir(), "missing.toml"), false))
}

func TestEnsureCLINotBehindScaffoldNoHamrSection(t *testing.T) {
	withCLIVersion(t, "1.0.0", true)
	dir := t.TempDir()
	path := filepath.Join(dir, "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte("[options]\ndatabase = \"postgres\"\n"), 0o600))

	require.NoError(t, ensureCLINotBehindScaffold(path, false))
}

// Both front ends must keep satisfying what runDevLoop needs.
var (
	_ devUI = headlessUI{}
	_ devUI = (*tui.Runtime)(nil)
)

func TestHeadlessUI_DockerStacksToStdout(t *testing.T) {
	sinks := headlessUI{}.RegisterDockerStacks([]string{"infra", "cache"})
	if len(sinks) != 2 || sinks["infra"] != os.Stdout || sinks["cache"] != os.Stdout {
		t.Fatalf("expected both stacks mapped to stdout, got %v", sinks)
	}
}
