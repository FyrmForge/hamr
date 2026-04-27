package devserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeArgs(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})

	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	dc := &DockerCompose{Name: "infra", File: "compose.yml"}

	t.Run("without override", func(t *testing.T) {
		assert.Equal(t, []string{"compose", "--project-directory", ".", "-f", "compose.yml"}, composeArgs(dc))
	})

	t.Run("with override", func(t *testing.T) {
		override := composeOverridePath(dc.Name)
		require.NoError(t, os.MkdirAll(filepath.Dir(override), 0o755))
		require.NoError(t, os.WriteFile(override, []byte("services: {}\n"), 0o644))
		assert.Equal(t, []string{"compose", "--project-directory", ".", "-f", "compose.yml", "-f", override}, composeArgs(dc))
	})

	t.Run("nested compose file keeps base compose directory", func(t *testing.T) {
		nested := &DockerCompose{Name: "infra", File: filepath.Join("docker", "compose.yml")}
		assert.Equal(t, []string{"compose", "--project-directory", "docker", "-f", filepath.Join("docker", "compose.yml"), "-f", composeOverridePath("infra")}, composeArgs(nested))
	})
}

func TestRewriteWebhookURLForAppPort(t *testing.T) {
	t.Run("localhost matching port rewrites", func(t *testing.T) {
		got := rewriteWebhookURLForAppPort("http://localhost:8080/stripe/webhook", 8080, 8081)
		assert.Equal(t, "http://localhost:8081/stripe/webhook", got)
	})

	t.Run("non-local host untouched", func(t *testing.T) {
		got := rewriteWebhookURLForAppPort("https://example.com/stripe/webhook", 8080, 8081)
		assert.Equal(t, "https://example.com/stripe/webhook", got)
	})

	t.Run("different port untouched", func(t *testing.T) {
		got := rewriteWebhookURLForAppPort("http://localhost:9999/stripe/webhook", 8080, 8081)
		assert.Equal(t, "http://localhost:9999/stripe/webhook", got)
	})
}

func TestParseComposeShortPort_IPv6Host(t *testing.T) {
	got, err := parseComposeShortPort("[::1]:5432:5432")
	require.NoError(t, err)
	assert.Equal(t, composePortBinding{
		HostIP:    "::1",
		HostPort:  5432,
		Container: 5432,
	}, got)
}

func TestEnsureDockerCompose_RemovesStaleOverrideWhenPortWalkDisabled(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})

	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	override := composeOverridePath("infra")
	require.NoError(t, os.MkdirAll(filepath.Dir(override), 0o755))
	require.NoError(t, os.WriteFile(override, []byte("services: {}\n"), 0o644))

	r := &Runner{
		cfg: &Config{
			Dev: DevConfig{
				PortWalk: func() *bool { b := false; return &b }(),
			},
		},
	}

	dc := &DockerCompose{Name: "infra", File: "compose.yml"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err = r.ensureDockerCompose(ctx, dc)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	_, statErr := os.Stat(override)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}
