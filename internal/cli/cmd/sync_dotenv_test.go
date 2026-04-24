package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadDotenvKey is the scoped replacement for the old loadDotenv side
// effect. It must NOT call os.Setenv — verified by checking that a key
// only present in the file does not appear in the process env after read.
func TestReadDotenvKey(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	const key = "HAMR_TEST_DOTENV_KEY_DO_NOT_OVERLAP"
	const val = "from_dotenv"

	require.NoError(t, os.WriteFile(envPath, []byte(
		"# a comment\n"+
			"\n"+
			"OTHER=ignored\n"+
			key+"="+val+"\n"+
			"QUOTED=\"with spaces\"\n"+
			"SINGLE_QUOTED='one'\n"+
			"BAD_LINE_NO_EQUALS\n",
	), 0o644))

	t.Run("hits read return value", func(t *testing.T) {
		got, ok := readDotenvKey(envPath, key)
		assert.True(t, ok)
		assert.Equal(t, val, got)
	})

	t.Run("does NOT mutate process env (the loadDotenv bug)", func(t *testing.T) {
		// Critical: the fix is "no os.Setenv at all". This guards the regression.
		_, _ = readDotenvKey(envPath, key)
		_, set := os.LookupEnv(key)
		assert.False(t, set, "readDotenvKey must not leak the value into os.Environ — that was the bug we fixed")
	})

	t.Run("misses return ok=false", func(t *testing.T) {
		_, ok := readDotenvKey(envPath, "NONEXISTENT_KEY_XYZ")
		assert.False(t, ok)
	})

	t.Run("strips matching surrounding quotes", func(t *testing.T) {
		got, ok := readDotenvKey(envPath, "QUOTED")
		require.True(t, ok)
		assert.Equal(t, "with spaces", got)
		got, ok = readDotenvKey(envPath, "SINGLE_QUOTED")
		require.True(t, ok)
		assert.Equal(t, "one", got)
	})

	t.Run("missing file returns ok=false (first-run case)", func(t *testing.T) {
		_, ok := readDotenvKey(filepath.Join(dir, "nonexistent.env"), key)
		assert.False(t, ok)
	})
}
