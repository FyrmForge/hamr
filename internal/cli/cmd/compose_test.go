package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickComposeEntry(t *testing.T) {
	one := []devserver.DockerCompose{{Name: "deps", File: "docker/docker-compose.yaml"}}
	two := append([]devserver.DockerCompose{{Name: "deps"}}, devserver.DockerCompose{Name: "infra"})

	t.Run("single entry needs no name", func(t *testing.T) {
		e, err := pickComposeEntry(one, "")
		require.NoError(t, err)
		assert.Equal(t, "deps", e.Name)
	})

	t.Run("named entry", func(t *testing.T) {
		e, err := pickComposeEntry(two, "infra")
		require.NoError(t, err)
		assert.Equal(t, "infra", e.Name)
	})

	t.Run("ambiguous without name", func(t *testing.T) {
		_, err := pickComposeEntry(two, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--name")
		assert.Contains(t, err.Error(), "deps, infra")
	})

	t.Run("unknown name lists the options", func(t *testing.T) {
		_, err := pickComposeEntry(two, "nope")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deps, infra")
	})

	t.Run("no entries at all", func(t *testing.T) {
		_, err := pickComposeEntry(nil, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no [[dev.docker_compose]] entries")
	})
}

// The whole point of the command: the override hamr dev generates has to end up
// in the docker args, which is exactly what a plain `docker compose -f base`
// call misses.
func TestComposeArgs_IncludesGeneratedOverride(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })

	dc := &devserver.DockerCompose{Name: "deps", File: "docker/docker-compose.yaml"}

	assert.Equal(t,
		[]string{"compose", "--project-directory", "docker", "-f", "docker/docker-compose.yaml"},
		devserver.ComposeArgs(dc),
		"no override on disk → base file only")

	override := filepath.Join(".hamr", "compose.deps.override.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(override), 0o755))
	require.NoError(t, os.WriteFile(override, []byte("services: {}\n"), 0o644))

	assert.Equal(t,
		[]string{"compose", "--project-directory", "docker", "-f", "docker/docker-compose.yaml", "-f", override},
		devserver.ComposeArgs(dc),
		"override on disk → merged on top")
}
