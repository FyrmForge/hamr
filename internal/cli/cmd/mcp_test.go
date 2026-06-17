package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeHandshake writes a .hamr/dev.json pointing at proxyURL with token.
func writeHandshake(t *testing.T, root, proxyURL, token string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hamr"), 0o755))
	body := `{"proxyURL":"` + proxyURL + `","token":"` + token + `"}`
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hamr", "dev.json"), []byte(body), 0o600))
}

func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, r.Content, 1)
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	return tc.Text
}

func TestBridgeCallForwards(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"rule":"go","text":"hi"}]`))
	}))
	defer srv.Close()

	root := t.TempDir()
	writeHandshake(t, root, srv.URL, "tok123")
	b := &mcpBridge{projectRoot: root, client: srv.Client()}

	res, err := b.call(context.Background(), "logs.read", []byte(`{"rule":"go"}`))
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Equal(t, "Bearer tok123", gotAuth)
	assert.Equal(t, "/__hamr/mcp/logs.read", gotPath)
	assert.JSONEq(t, `{"rule":"go"}`, gotBody)
	assert.Contains(t, resultText(t, res), `"rule":"go"`)
}

func TestBridgeCallDevNotRunning(t *testing.T) {
	b := &mcpBridge{projectRoot: t.TempDir(), client: http.DefaultClient}
	res, err := b.call(context.Background(), "dev.info", nil)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "not running")
}

func TestBridgeCallGatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"mcp gateway is off"}`))
	}))
	defer srv.Close()

	root := t.TempDir()
	writeHandshake(t, root, srv.URL, "tok")
	b := &mcpBridge{projectRoot: root, client: srv.Client()}

	res, err := b.call(context.Background(), "make.run", []byte(`{"target":"build"}`))
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "mcp gateway is off")
}

func TestResolveProjectRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "hamr.toml"), []byte("# test\n"), 0o644))

	// --project flag, valid + invalid.
	got, err := resolveProjectRoot(root)
	require.NoError(t, err)
	assert.Equal(t, root, got)
	_, err = resolveProjectRoot(t.TempDir())
	assert.Error(t, err, "dir without hamr.toml")

	// cwd walk-up from a nested subdir.
	sub := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)
	got, err = resolveProjectRoot("")
	require.NoError(t, err)
	gotResolved, _ := filepath.EvalSymlinks(got)
	rootResolved, _ := filepath.EvalSymlinks(root)
	assert.Equal(t, rootResolved, gotResolved)
}
