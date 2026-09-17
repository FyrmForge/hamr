package devserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadDotenvKey guards the one .env parser hamr dev and `hamr sync` share.
// It must read what godotenv hands the app and never call os.Setenv.
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
			"export EXPORTED=exp_value\n"+
			"BAD_LINE_NO_EQUALS\n"+
			"COMMENTED=sk_test_abc # sandbox\n"+
			"TAB_COMMENTED=abc\t# note\n"+
			"HASH_IN_VALUE=abc#def\n"+
			"QUOTED_COMMENTED=\"a # b\" # see \"docs\"\n"+
			"ESCAPED=\"a\\\"b\"\n"+
			"SINGLE_ESCAPED='p\\'w@h'\n",
	), 0o644))

	for k, want := range map[string]string{
		key:                val,
		"QUOTED":           "with spaces",
		"SINGLE_QUOTED":    "one",
		"EXPORTED":         "exp_value",
		"COMMENTED":        "sk_test_abc",
		"TAB_COMMENTED":    "abc",
		"HASH_IN_VALUE":    "abc#def",
		"QUOTED_COMMENTED": "a # b",
		"ESCAPED":          `a\"b`,
		"SINGLE_ESCAPED":   `p\'w@h`,
	} {
		got, ok := ReadDotenvKey(envPath, k)
		assert.True(t, ok, k)
		assert.Equal(t, want, got, k)
	}

	_, set := os.LookupEnv(key)
	assert.False(t, set, "ReadDotenvKey must not leak values into os.Environ")

	_, ok := ReadDotenvKey(envPath, "NONEXISTENT_KEY_XYZ")
	assert.False(t, ok)
	_, ok = ReadDotenvKey(filepath.Join(dir, "nonexistent.env"), key)
	assert.False(t, ok, "missing file is a miss, not an error")

	// The port-walk parser shares the line parser, so `export` keys keep their name.
	entries := parseDotenv([]byte("export DATABASE_URL=postgres://localhost:5432/db\n"))
	require.Len(t, entries, 1)
	assert.Equal(t, "DATABASE_URL", entries[0].Key)
}
