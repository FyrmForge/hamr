package devserver

import (
	"context"
	"net"
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

func TestComposeArgsForInspect_omitsOverride(t *testing.T) {
	// Inspect path must never include the generated override file. If
	// the user renames or removes a service in the base file while the
	// previous override still references it, including the override
	// would make `compose ps` fail to merge config and hard-fail before
	// we get to reconcile the override.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	dc := &DockerCompose{Name: "infra", File: "compose.yml"}

	override := composeOverridePath(dc.Name)
	require.NoError(t, os.MkdirAll(filepath.Dir(override), 0o755))
	require.NoError(t, os.WriteFile(override, []byte("services: {}\n"), 0o644))

	got := composeArgsForInspect(dc)
	assert.Equal(t, []string{"compose", "--project-directory", ".", "-f", "compose.yml"}, got)
	assert.NotContains(t, got, override, "inspect must not pass the override file to compose")
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

func TestParseComposePorts_capturesProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yml")
	yaml := `services:
  db:
    image: postgres
    ports:
      - "5432:5432"
  seed:
    image: alpine
    profiles: [tools]
  shell:
    image: alpine
    profiles:
      - tools
      - debug
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	got, err := parseComposePorts(path)
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.Equal(t, "db", got[0].Name)
	assert.Empty(t, got[0].Profiles, "db has no profiles → always started")

	assert.Equal(t, "seed", got[1].Name)
	assert.Equal(t, []string{"tools"}, got[1].Profiles)

	assert.Equal(t, "shell", got[2].Name)
	assert.Equal(t, []string{"tools", "debug"}, got[2].Profiles)
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

func TestWalkComposeServices_skipsProbeForOwnedPorts(t *testing.T) {
	// Bind a real listener so the port is genuinely busy from the OS's
	// perspective. The owned set tells the walk to treat it as ours and
	// return it unchanged — without owned-set awareness, the walk would
	// see EADDRINUSE and shift +1.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	services := []composeService{
		{
			Name: "db",
			Ports: []composePortBinding{
				{HostPort: port, Container: 5432},
			},
		},
	}
	owned := map[string]bool{hostPortKey("127.0.0.1", port): true}

	updated, shifts := walkComposeServices(services, owned, 5, nil)
	assert.Empty(t, shifts, "owned port must not produce a shift")
	require.Len(t, updated, 1)
	require.Len(t, updated[0].Ports, 1)
	assert.Equal(t, port, updated[0].Ports[0].HostPort, "owned port must stay unchanged")
}

func TestWalkComposeServices_walksWhenPortBusyAndNotOwned(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	services := []composeService{
		{
			Name: "db",
			Ports: []composePortBinding{
				{HostPort: port, Container: 5432},
			},
		},
	}

	updated, shifts := walkComposeServices(services, nil, 5, nil)
	require.Len(t, shifts, 1)
	assert.Equal(t, "db", shifts[0].Service)
	assert.Equal(t, port, shifts[0].Old)
	assert.Greater(t, shifts[0].New, port)
	require.Len(t, updated, 1)
	assert.Equal(t, shifts[0].New, updated[0].Ports[0].HostPort)
}

func TestEnsureDockerCompose_HardFailsOnDockerError(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})

	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	// Stale override on disk from a prior session. Under the state-
	// aware design we no longer auto-remove it just because port_walk
	// is disabled — only a successful inspect+apply with a confirmed-
	// empty project state may clear it. A hard fail from `docker
	// compose ps` (here: cancelled context) leaves the override alone.
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

	// The override file is preserved when the inspect step fails —
	// state-aware management never throws away encoded port mappings
	// on a transient docker-side error.
	_, statErr := os.Stat(override)
	assert.NoError(t, statErr, "override file should be preserved on inspect failure")
}
