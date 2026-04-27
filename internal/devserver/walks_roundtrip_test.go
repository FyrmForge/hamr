package devserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWalksRoundTrip_WriterToResolver pins the contract between the writer
// side of port-walk persistence (hamr dev → writeWalks) and the reader side
// (`hamr env` / `hamr sync` → ResolveEnvRewrites). Existing env_test.go
// reads hand-written walks.json, so it can drift from the writer when the
// JSON schema evolves; this test uses the actual writer to produce the
// file and the actual public resolver to consume it. If a future change
// renames a JSON tag on one side and forgets the other, this test breaks.
//
// Coverage in one shot: every match rule (localhost, 127.0.0.1, 0.0.0.0,
// [::1], whole-value :port), pass-through cases (remote host, unrelated
// value, port-omitted URL), declaration-order preservation, and a
// multi-source shift list (proxy + app + multiple compose entries).
func TestWalksRoundTrip_WriterToResolver(t *testing.T) {
	dir := t.TempDir()

	envContent := strings.Join([]string{
		"DATABASE_URL=postgres://postgres:postgres@localhost:5432/myapp",
		"S3_ENDPOINT=http://127.0.0.1:9000",
		"REDIS_URL=redis://0.0.0.0:6379",
		"IPV6_TARGET=http://[::1]:9000",
		"LISTEN_ADDR=:8080",
		"LOG_LEVEL=info",
		"REMOTE_DB=postgres://user:pass@db.example.com:5432/db",
		"NO_PORT_URL=postgres://user@localhost/db",
	}, "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0o644))

	records := []portShiftRecord{
		{Kind: "proxy", Old: 3000, New: 3001},
		{Kind: "app", Old: 8080, New: 8081},
		{Kind: "compose", ComposeName: "deps", Service: "postgres", HostIP: "127.0.0.1", Old: 5432, New: 5433},
		{Kind: "compose", ComposeName: "deps", Service: "rustfs", Old: 9000, New: 9001},
		{Kind: "compose", ComposeName: "cache", Service: "redis", Old: 6379, New: 6380},
	}
	require.NoError(t, writeWalks(dir, records))

	rewrites, err := ResolveEnvRewrites(dir)
	require.NoError(t, err)

	// Order must match .env declaration order so deterministic shell
	// sourcing is reproducible — repeated `eval $(hamr env --export)`
	// calls produce the same byte sequence.
	assert.Equal(t, []string{
		"DATABASE_URL=postgres://postgres:postgres@localhost:5433/myapp",
		"S3_ENDPOINT=http://127.0.0.1:9001",
		"REDIS_URL=redis://0.0.0.0:6380",
		"IPV6_TARGET=http://[::1]:9001",
		"LISTEN_ADDR=:8081",
	}, rewrites, "writer→reader output must include every matched .env entry, in declaration order, and only those")
}

// TestWalksRoundTrip_SingleValueResolver covers the `hamr sync` path that
// rewrites a single .env-derived value (S3_ENDPOINT) instead of the full
// dotenv list. Mirrors the writer→reader pinning above for that code
// path so flagOrEnv stays in sync with hamr dev's persistence.
func TestWalksRoundTrip_SingleValueResolver(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeWalks(dir, []portShiftRecord{
		{Kind: "compose", ComposeName: "deps", Service: "rustfs", Old: 9000, New: 9001},
	}))

	t.Run("matched value is rewritten", func(t *testing.T) {
		assert.Equal(t, "http://localhost:9001", RewriteValueForWalks(dir, "http://localhost:9000"))
	})

	t.Run("unrelated port is pass-through", func(t *testing.T) {
		assert.Equal(t, "http://localhost:8025", RewriteValueForWalks(dir, "http://localhost:8025"))
	})

	t.Run("missing walks file is pass-through", func(t *testing.T) {
		// Fresh tempdir — no walks.json. Resolver must not error out;
		// the literal value the caller already has is the right answer.
		empty := t.TempDir()
		assert.Equal(t, "http://localhost:9000", RewriteValueForWalks(empty, "http://localhost:9000"))
	})
}
