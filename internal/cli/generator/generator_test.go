package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendMinIO_addsServiceAndVolume(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker", "docker-compose.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(compose), 0o755))
	require.NoError(t, os.WriteFile(compose, []byte("services:\n  app:\n    image: app\n"), 0o644))

	// chdir so appendMinIO finds "docker/docker-compose.yaml"
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(orig) }()

	require.NoError(t, appendMinIO(&ServiceConfig{}))

	got, err := os.ReadFile(compose)
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "minio:")
	assert.Contains(t, content, "minio_data:/data")
	assert.Contains(t, content, "volumes:")
	assert.Contains(t, content, "minio_data:")
}

func TestAppendMinIO_idempotent(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker", "docker-compose.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(compose), 0o755))
	require.NoError(t, os.WriteFile(compose, []byte("services:\n  app:\n    image: app\n"), 0o644))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(orig) }()

	require.NoError(t, appendMinIO(&ServiceConfig{}))
	require.NoError(t, appendMinIO(&ServiceConfig{}))

	got, err := os.ReadFile(compose)
	require.NoError(t, err)
	content := string(got)

	// "minio:" should appear exactly once (the service definition).
	assert.Equal(t, 1, strings.Count(content, "  minio:\n"))
}

func TestAppendMinIO_existingVolumesSection(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "docker", "docker-compose.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(compose), 0o755))

	initial := "services:\n  app:\n    image: app\n\nvolumes:\n  pg_data:\n"
	require.NoError(t, os.WriteFile(compose, []byte(initial), 0o644))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(orig) }()

	require.NoError(t, appendMinIO(&ServiceConfig{}))

	got, err := os.ReadFile(compose)
	require.NoError(t, err)
	content := string(got)

	// Must not produce a duplicate "volumes:" top-level key.
	assert.Equal(t, 1, strings.Count(content, "\nvolumes:\n"),
		"should not duplicate the volumes section")
	assert.Contains(t, content, "minio_data:")
	assert.Contains(t, content, "pg_data:")
}

func TestAppendMinIO_noComposeFile(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer func() { _ = os.Chdir(orig) }()

	// No docker-compose.yaml — should silently succeed.
	assert.NoError(t, appendMinIO(&ServiceConfig{}))
}
