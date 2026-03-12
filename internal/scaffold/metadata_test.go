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
cmd = "go build -o ./bin/app ./cmd/site"
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

func TestAIDir(t *testing.T) {
	t.Run("returns configured dir", func(t *testing.T) {
		meta := Metadata{AI: AIConfig{Dir: "custom/ai"}}
		assert.Equal(t, "custom/ai", meta.AIDir())
	})

	t.Run("returns default when empty", func(t *testing.T) {
		meta := Metadata{}
		assert.Equal(t, DefaultAIDir, meta.AIDir())
	})
}

func TestResolveAIDir(t *testing.T) {
	t.Run("reads from toml", func(t *testing.T) {
		content := `
[ai]
dir = "my/ai/dir"
`
		path := writeTempTOML(t, content)
		assert.Equal(t, "my/ai/dir", ResolveAIDir(path))
	})

	t.Run("falls back on missing file", func(t *testing.T) {
		assert.Equal(t, DefaultAIDir, ResolveAIDir("/nonexistent/hamr.toml"))
	})

	t.Run("falls back when no ai section", func(t *testing.T) {
		content := `
[hamr]
version = "0.3.2"
`
		path := writeTempTOML(t, content)
		assert.Equal(t, DefaultAIDir, ResolveAIDir(path))
	})

	t.Run("falls back on unparseable file", func(t *testing.T) {
		path := writeTempTOML(t, `[hamr
broken toml here`)
		assert.Equal(t, DefaultAIDir, ResolveAIDir(path))
	})
}

func writeTempTOML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hamr.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}
