package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateVersion(t *testing.T) {
	t.Run("updates version line", func(t *testing.T) {
		content := `[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"

[proxy]
listen = ":3000"
`
		path := writeTempTOML(t, content)
		require.NoError(t, UpdateVersion(path, "0.5.0"))

		updated, err := os.ReadFile(path)
		require.NoError(t, err)

		assert.Contains(t, string(updated), `version = "0.5.0"`)
		assert.Contains(t, string(updated), `scaffolded_at = "2026-03-11"`)
		assert.Contains(t, string(updated), `database = "postgres"`)
		assert.Contains(t, string(updated), `listen = ":3000"`)
	})

	t.Run("preserves comments", func(t *testing.T) {
		content := `[hamr]
version = "0.3.2"
# This is a comment
scaffolded_at = "2026-03-11"

[proxy]
listen = ":3000"
`
		path := writeTempTOML(t, content)
		require.NoError(t, UpdateVersion(path, "1.0.0"))

		updated, err := os.ReadFile(path)
		require.NoError(t, err)

		assert.Contains(t, string(updated), "# This is a comment")
		assert.Contains(t, string(updated), `version = "1.0.0"`)
	})

	t.Run("missing hamr section inserts it", func(t *testing.T) {
		content := `[proxy]
listen = ":3000"
`
		path := writeTempTOML(t, content)
		require.NoError(t, UpdateVersion(path, "1.0.0"))

		updated, err := os.ReadFile(path)
		require.NoError(t, err)

		assert.Contains(t, string(updated), `[hamr]`)
		assert.Contains(t, string(updated), `version = "1.0.0"`)
		assert.Contains(t, string(updated), `listen = ":3000"`)
	})

	t.Run("missing file", func(t *testing.T) {
		err := UpdateVersion(filepath.Join(t.TempDir(), "nonexistent.toml"), "1.0.0")
		require.Error(t, err)
	})
}
