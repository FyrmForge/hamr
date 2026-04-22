package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/FyrmForge/hamr/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withFetchFn swaps fetchLatestReleaseFn for a test.
func withFetchFn(t *testing.T, fn func(context.Context) (scaffold.Version, error)) {
	t.Helper()
	orig := fetchLatestReleaseFn
	fetchLatestReleaseFn = fn
	t.Cleanup(func() { fetchLatestReleaseFn = orig })
}

// captureStderr temporarily redirects os.Stderr so tests can assert on warning output.
func captureStderr(t *testing.T) (restore func() string) {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	return func() string {
		_ = w.Close()
		os.Stderr = orig
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return buf.String()
	}
}

func TestEnsureLatestCLIBlocksWhenOlder(t *testing.T) {
	withCLIVersion(t, "1.0.0", true)
	withFetchFn(t, func(context.Context) (scaffold.Version, error) {
		return scaffold.Version{Major: 1, Minor: 1, Patch: 0}, nil
	})

	err := ensureLatestCLI(context.Background(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v1.0.0")
	assert.Contains(t, err.Error(), "v1.1.0")
	assert.Contains(t, err.Error(), "--skip-version-check")
}

func TestEnsureLatestCLIAllowsWhenEqual(t *testing.T) {
	withCLIVersion(t, "1.2.3", true)
	withFetchFn(t, func(context.Context) (scaffold.Version, error) {
		return scaffold.Version{Major: 1, Minor: 2, Patch: 3}, nil
	})

	require.NoError(t, ensureLatestCLI(context.Background(), false))
}

func TestEnsureLatestCLIAllowsWhenAhead(t *testing.T) {
	withCLIVersion(t, "2.0.0", true)
	withFetchFn(t, func(context.Context) (scaffold.Version, error) {
		return scaffold.Version{Major: 1, Minor: 9, Patch: 9}, nil
	})

	require.NoError(t, ensureLatestCLI(context.Background(), false))
}

func TestEnsureLatestCLISkippedByFlag(t *testing.T) {
	withCLIVersion(t, "0.1.0", true)
	called := false
	withFetchFn(t, func(context.Context) (scaffold.Version, error) {
		called = true
		return scaffold.Version{Major: 9, Minor: 0, Patch: 0}, nil
	})

	require.NoError(t, ensureLatestCLI(context.Background(), true))
	assert.False(t, called, "fetch should not be called when skip=true")
}

func TestEnsureLatestCLISkippedOnDevBuild(t *testing.T) {
	withCLIVersion(t, "dev", false)
	called := false
	withFetchFn(t, func(context.Context) (scaffold.Version, error) {
		called = true
		return scaffold.Version{Major: 9, Minor: 0, Patch: 0}, nil
	})

	require.NoError(t, ensureLatestCLI(context.Background(), false))
	assert.False(t, called, "fetch should not be called on dev builds")
}

func TestEnsureLatestCLIWarnsOnNetworkError(t *testing.T) {
	withCLIVersion(t, "1.0.0", true)
	withFetchFn(t, func(context.Context) (scaffold.Version, error) {
		return scaffold.Version{}, errors.New("network unreachable")
	})

	getStderr := captureStderr(t)
	err := ensureLatestCLI(context.Background(), false)
	stderr := getStderr()

	require.NoError(t, err, "network errors must not block scaffolding")
	assert.Contains(t, stderr, "warning")
	assert.Contains(t, stderr, "network unreachable")
}

func TestEnsureLatestCLIWarnsOnUnparseableCurrentVersion(t *testing.T) {
	withCLIVersion(t, "garbage", true)
	called := false
	withFetchFn(t, func(context.Context) (scaffold.Version, error) {
		called = true
		return scaffold.Version{}, nil
	})

	getStderr := captureStderr(t)
	err := ensureLatestCLI(context.Background(), false)
	stderr := getStderr()

	require.NoError(t, err)
	assert.False(t, called, "fetch should not run when current version is unparseable")
	assert.Contains(t, stderr, "warning")
}

func TestEnsureLatestCLIHandlesNilContext(t *testing.T) {
	withCLIVersion(t, "1.0.0", true)
	var gotCtx context.Context
	withFetchFn(t, func(ctx context.Context) (scaffold.Version, error) {
		gotCtx = ctx
		return scaffold.Version{Major: 1, Minor: 0, Patch: 0}, nil
	})

	// cobra.Command.Context() returns nil for commands that have never been
	// executed — this guards the direct-RunE test path.
	var nilCtx context.Context
	require.NoError(t, ensureLatestCLI(nilCtx, false))
	require.NotNil(t, gotCtx, "ensureLatestCLI must supply a non-nil context")
}
