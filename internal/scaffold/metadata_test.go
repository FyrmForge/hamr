package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMetadata(t *testing.T) {
	t.Run("full hamr.toml", func(t *testing.T) {
		content := `
[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"
db_connector = "sqlx"
auth = "session"
css = "tailwind"
websockets = true
e2e = false
stripe = true
locale = false
storage = "local"

[proxy]
listen = ":3000"
`
		path := writeTempTOML(t, content)

		meta, err := LoadMetadata(path)
		require.NoError(t, err)

		assert.True(t, meta.HasHamrSection())
		assert.Equal(t, "0.3.2", meta.Hamr.Version)
		assert.Equal(t, "2026-03-11", meta.Hamr.ScaffoldedAt)
		assert.Equal(t, "postgres", meta.Options.Database)
		assert.Equal(t, "sqlx", meta.Options.DBConnector)
		assert.Equal(t, "session", meta.Options.Auth)
		assert.Equal(t, "tailwind", meta.Options.CSS)
		assert.True(t, meta.Options.WebSockets)
		assert.False(t, meta.Options.E2E)
		assert.True(t, meta.Options.Stripe)
		assert.False(t, meta.Options.Locale)
		assert.Equal(t, "local", meta.Options.Storage)
	})

	t.Run("no hamr section", func(t *testing.T) {
		content := `
[proxy]
listen = ":3000"

[[dev.watch]]
name = "site"
watch = "**/*.go"
cmd = "go build -o ./bin/app ./cmd/server"
`
		path := writeTempTOML(t, content)

		meta, err := LoadMetadata(path)
		require.NoError(t, err)

		assert.False(t, meta.HasHamrSection())
		assert.Empty(t, meta.Hamr.Version)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadMetadata("/nonexistent/hamr.toml")
		require.Error(t, err)
	})

	t.Run("invalid toml", func(t *testing.T) {
		path := writeTempTOML(t, `[hamr
broken`)
		_, err := LoadMetadata(path)
		require.Error(t, err)
	})
}

func writeTempTOML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}
