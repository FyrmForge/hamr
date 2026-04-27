package devserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteForPortShifts(t *testing.T) {
	shifts := portShifts{5432: 5433, 9000: 9001, 8080: 8081}

	tests := []struct {
		name    string
		entries []dotenvEntry
		want    []string
	}{
		{
			name:    "localhost host:port URL is rewritten",
			entries: []dotenvEntry{{Key: "DATABASE_URL", Value: "postgres://postgres:postgres@localhost:5432/myapp?sslmode=disable"}},
			want:    []string{"DATABASE_URL=postgres://postgres:postgres@localhost:5433/myapp?sslmode=disable"},
		},
		{
			name:    "127.0.0.1 host:port is rewritten",
			entries: []dotenvEntry{{Key: "S3_ENDPOINT", Value: "http://127.0.0.1:9000"}},
			want:    []string{"S3_ENDPOINT=http://127.0.0.1:9001"},
		},
		{
			name:    "0.0.0.0 host:port is rewritten",
			entries: []dotenvEntry{{Key: "REDIS", Value: "redis://0.0.0.0:5432"}},
			want:    []string{"REDIS=redis://0.0.0.0:5433"},
		},
		{
			name:    "[::1] host:port is rewritten",
			entries: []dotenvEntry{{Key: "PG", Value: "postgres://[::1]:5432/db"}},
			want:    []string{"PG=postgres://[::1]:5433/db"},
		},
		{
			name:    "whole-value :port form is rewritten (Go listener convention)",
			entries: []dotenvEntry{{Key: "LISTEN_ADDR", Value: ":8080"}},
			want:    []string{"LISTEN_ADDR=:8081"},
		},
		{
			name:    "port not in shifts: untouched",
			entries: []dotenvEntry{{Key: "MAILHOG", Value: "http://localhost:8025"}},
			want:    nil,
		},
		{
			name:    "remote host: untouched (only local-host prefixes match)",
			entries: []dotenvEntry{{Key: "DATABASE_URL", Value: "postgres://user:pass@db.prod.example.com:5432/app"}},
			want:    nil,
		},
		{
			name:    "userinfo with :port-shaped password does not false-positive",
			entries: []dotenvEntry{{Key: "DATABASE_URL", Value: "postgres://user:5432pass@localhost:5432/db"}},
			want:    []string{"DATABASE_URL=postgres://user:5432pass@localhost:5433/db"},
		},
		{
			name:    "port omitted from URL: not rewritten (documented limitation)",
			entries: []dotenvEntry{{Key: "DATABASE_URL", Value: "postgres://user@localhost/db"}},
			want:    nil,
		},
		{
			name:    "bare numeric value does not match :port form",
			entries: []dotenvEntry{{Key: "RETRY_COUNT", Value: "5432"}},
			want:    nil,
		},
		{
			name:    "longer port not confused with prefix (5432 does not match 54321)",
			entries: []dotenvEntry{{Key: "WEIRD", Value: "http://localhost:54321"}},
			want:    nil,
		},
		{
			name: "multiple shifts in one value",
			entries: []dotenvEntry{
				{Key: "MULTI", Value: "postgres://localhost:5432/db|http://localhost:9000"},
			},
			want: []string{"MULTI=postgres://localhost:5433/db|http://localhost:9001"},
		},
		{
			name: "cascading shifts (5432→5433, 5433→5434) handled in single pass",
			entries: []dotenvEntry{
				{Key: "DATABASE_URL", Value: "postgres://localhost:5432/db"},
			},
			// Even with a cascade in shifts, localhost:5432 should map to 5433
			// (not 5434) — single-pass via regex callback prevents re-substitution.
			want: []string{"DATABASE_URL=postgres://localhost:5433/db"},
		},
		{
			name:    "unchanged entries are omitted from output",
			entries: []dotenvEntry{{Key: "STATIC_URL", Value: "https://example.com/static"}},
			want:    nil,
		},
		{
			name: "mixed entries: only changed ones are emitted",
			entries: []dotenvEntry{
				{Key: "DATABASE_URL", Value: "postgres://localhost:5432/db"},
				{Key: "LOG_LEVEL", Value: "info"},
				{Key: "S3_ENDPOINT", Value: "http://localhost:9000"},
			},
			want: []string{
				"DATABASE_URL=postgres://localhost:5433/db",
				"S3_ENDPOINT=http://localhost:9001",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteForPortShifts(tt.entries, shifts)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRewriteForPortShifts_CascadeDoesNotCompound(t *testing.T) {
	// Independent verification of the no-double-substitution guarantee.
	// If applyShiftsToValue ran one shift at a time across the value, the
	// 5432 → 5433 substitution would feed the 5433 → 5434 substitution and
	// produce ":5434" — wrong. Single-pass regex callback prevents this.
	shifts := portShifts{5432: 5433, 5433: 5434}
	got := rewriteForPortShifts(
		[]dotenvEntry{{Key: "X", Value: "localhost:5432"}},
		shifts,
	)
	require.Equal(t, []string{"X=localhost:5433"}, got)
}

func TestRewriteForPortShifts_NoShifts(t *testing.T) {
	got := rewriteForPortShifts(
		[]dotenvEntry{{Key: "X", Value: "localhost:5432"}},
		nil,
	)
	assert.Nil(t, got)
}

func TestRewriteForPortShifts_NoEntries(t *testing.T) {
	got := rewriteForPortShifts(nil, portShifts{5432: 5433})
	assert.Nil(t, got)
}

func TestLoadDotenv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	t.Run("missing file returns nil error", func(t *testing.T) {
		entries, err := loadDotenv(filepath.Join(dir, "nonexistent.env"))
		require.NoError(t, err)
		assert.Nil(t, entries)
	})

	t.Run("parses ordered entries", func(t *testing.T) {
		require.NoError(t, os.WriteFile(envPath, []byte(
			"# header comment\n"+
				"\n"+
				"DATABASE_URL=postgres://localhost:5432/db\n"+
				"S3_ENDPOINT=\"http://localhost:9000\"\n"+
				"QUOTED='single quoted'\n"+
				"BAD_LINE_NO_EQUALS\n"+
				"PORT=8080\n",
		), 0o644))

		got, err := loadDotenv(envPath)
		require.NoError(t, err)
		require.Len(t, got, 4)
		assert.Equal(t, "DATABASE_URL", got[0].Key)
		assert.Equal(t, "postgres://localhost:5432/db", got[0].Value)
		assert.Equal(t, "S3_ENDPOINT", got[1].Key)
		assert.Equal(t, "http://localhost:9000", got[1].Value, "double-quotes stripped")
		assert.Equal(t, "QUOTED", got[2].Key)
		assert.Equal(t, "single quoted", got[2].Value, "single-quotes stripped")
		assert.Equal(t, "PORT", got[3].Key)
		assert.Equal(t, "8080", got[3].Value)
	})
}
