package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunEnv_NoWalksFile(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer
	require.NoError(t, runEnv(dir, false, &out))
	assert.Empty(t, out.String(), "missing walks.json must exit clean with no output so eval is a no-op")
}

func TestRunEnv_WalksAppliedToDotenv(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".hamr"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".hamr", "walks.json"), []byte(`{
  "shifts": [
    {"kind": "compose", "compose_name": "deps", "service": "postgres", "old": 5432, "new": 5433},
    {"kind": "compose", "compose_name": "deps", "service": "rustfs", "old": 9000, "new": 9001}
  ]
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"DATABASE_URL=postgres://postgres:postgres@localhost:5432/myapp?sslmode=disable\n"+
			"S3_ENDPOINT=http://localhost:9000\n"+
			"LOG_LEVEL=info\n",
	), 0o644))

	t.Run("plain output", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, runEnv(dir, false, &out))
		got := out.String()
		assert.Contains(t, got, "DATABASE_URL=postgres://postgres:postgres@localhost:5433/myapp?sslmode=disable")
		assert.Contains(t, got, "S3_ENDPOINT=http://localhost:9001")
		assert.NotContains(t, got, "LOG_LEVEL", "unchanged values must not be emitted")
	})

	t.Run("--export form quotes values for shell sourcing", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, runEnv(dir, true, &out))
		got := out.String()
		assert.Contains(t, got, "export DATABASE_URL='postgres://postgres:postgres@localhost:5433/myapp?sslmode=disable'")
		assert.Contains(t, got, "export S3_ENDPOINT='http://localhost:9001'")
	})
}

func TestRunEnv_NoMatchingShifts(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".hamr"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".hamr", "walks.json"), []byte(`{
  "shifts": [{"kind": "compose", "service": "redis", "old": 6379, "new": 6380}]
}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"DATABASE_URL=postgres://localhost:5432/db\n",
	), 0o644))

	var out bytes.Buffer
	require.NoError(t, runEnv(dir, false, &out))
	assert.Empty(t, out.String(), ".env values that don't reference any walked port must produce no output")
}

func TestShellSingleQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "simple", want: "'simple'"},
		{in: "has spaces", want: "'has spaces'"},
		{in: "postgres://user:pass@localhost:5432/db", want: "'postgres://user:pass@localhost:5432/db'"},
		{in: "with'quote", want: `'with'\''quote'`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, shellSingleQuote(tt.in))
		})
	}
}
