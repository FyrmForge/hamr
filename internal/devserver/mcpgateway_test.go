package devserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestGateway(t *testing.T, access map[string]string) *mcpGateway {
	t.Helper()
	cfg := &Config{Dev: DevConfig{
		Watch: []WatchRule{{Name: "go"}},
		MCP:   MCPConfig{Enabled: true, Access: access},
	}}
	logBuf := NewLogBuffer(100)
	logBuf.Append(LogLine{Rule: "go", Text: "hello world"})
	logBuf.Append(LogLine{Rule: "templ", Text: "other line"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	g, err := newMCPGateway(mcpGatewayDeps{
		cfg:        cfg,
		actions:    &DevActions{cfg: cfg, logger: logger},
		logBuf:     logBuf,
		errorState: NewErrorState(),
		proxyURL:   "http://localhost:3000",
		appPort:    8080,
		makefile:   "Makefile",
		logger:     logger,
	})
	require.NoError(t, err)
	return g
}

func doMCP(g *mcpGateway, tool, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/__hamr/mcp/"+tool, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	g.handle(rec, req)
	return rec
}

func TestGatewayAuth(t *testing.T) {
	g := newTestGateway(t, map[string]string{"dev": "read"})
	g.enabled.Store(true)

	assert.Equal(t, http.StatusUnauthorized, doMCP(g, "dev.info", "", "").Code, "no token")
	assert.Equal(t, http.StatusUnauthorized, doMCP(g, "dev.info", "wrong-token", "").Code, "bad token")
}

func TestGatewayKillSwitch(t *testing.T) {
	g := newTestGateway(t, map[string]string{"dev": "read"})

	g.enabled.Store(false)
	assert.Equal(t, http.StatusForbidden, doMCP(g, "dev.info", g.token, "").Code, "gateway off")

	g.enabled.Store(true)
	assert.Equal(t, http.StatusOK, doMCP(g, "dev.info", g.token, "").Code, "gateway on")
}

func TestGatewayPermissionEnforcement(t *testing.T) {
	// dev+logs read granted, docker not granted at all.
	g := newTestGateway(t, map[string]string{"dev": "read", "logs": "read"})
	g.enabled.Store(true)

	assert.Equal(t, http.StatusForbidden, doMCP(g, "docker.wipe", g.token, `{"name":"infra"}`).Code, "ungranted tool")
	assert.Equal(t, http.StatusOK, doMCP(g, "logs.read", g.token, "").Code, "granted tool")
}

func TestGatewayLogsRead(t *testing.T) {
	g := newTestGateway(t, map[string]string{"logs": "read"})
	g.enabled.Store(true)

	rec := doMCP(g, "logs.read", g.token, `{"rule":"go"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "hello world")
	assert.NotContains(t, body, "other line", "rule filter should exclude templ")
}

func TestGatewayToggleManagesHandshake(t *testing.T) {
	t.Chdir(t.TempDir()) // writeHandshake is cwd-relative
	g := newTestGateway(t, map[string]string{"dev": "read"})
	assert.False(t, g.IsEnabled())

	on, err := g.Toggle()
	require.NoError(t, err)
	assert.True(t, on)
	assert.True(t, g.IsEnabled())
	assert.FileExists(t, MCPHandshakeFile)

	off, err := g.Toggle()
	require.NoError(t, err)
	assert.False(t, off)
	assert.NoFileExists(t, MCPHandshakeFile, "handshake removed on disable")
}

func TestGatewayAttributesMutations(t *testing.T) {
	t.Chdir(t.TempDir())
	var logOut bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOut, nil))
	cfg := &Config{Dev: DevConfig{MCP: MCPConfig{
		Enabled: true,
		Access:  map[string]string{"mail": "write", "logs": "read"},
	}}}
	g, err := newMCPGateway(mcpGatewayDeps{
		cfg:        cfg,
		actions:    &DevActions{cfg: cfg, logger: logger},
		logBuf:     NewLogBuffer(100),
		errorState: NewErrorState(),
		proxyURL:   "http://localhost:3000",
		makefile:   "Makefile",
		logger:     logger,
	})
	require.NoError(t, err)
	require.NoError(t, g.SetActive(true))

	require.Equal(t, http.StatusOK, doMCP(g, "logs.read", g.token, "").Code)  // read
	require.Equal(t, http.StatusOK, doMCP(g, "mail.clear", g.token, "").Code) // mutation

	out := logOut.String()
	assert.Contains(t, out, "mail.clear", "mutation attributed via dev logger")
	assert.NotContains(t, out, "logs.read", "reads are not attributed")
}

func TestGatewayAuditsDenials(t *testing.T) {
	g := newTestGateway(t, map[string]string{"dev": "read"})
	audit, err := newMCPAuditLog("") // file-less; capture via sink
	require.NoError(t, err)
	var lines []string
	audit.setSink(func(s string) { lines = append(lines, s) })
	g.audit.Store(audit)

	g.enabled.Store(true)
	doMCP(g, "docker.wipe", g.token, `{"name":"infra"}`) // not permitted
	doMCP(g, "dev.info", "badtoken", "")                 // unauthorized
	g.enabled.Store(false)
	doMCP(g, "dev.info", g.token, "") // gateway off

	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "docker.wipe → DENIED: not permitted")
	assert.Contains(t, joined, "dev.info → DENIED: unauthorized")
	assert.Contains(t, joined, "dev.info → DENIED: gateway off")
}

func TestGatewayDevInfo(t *testing.T) {
	g := newTestGateway(t, map[string]string{"dev": "read"})
	g.enabled.Store(true)

	rec := doMCP(g, "dev.info", g.token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"proxyURL":"http://localhost:3000"`)
	assert.Contains(t, body, `"go"`)
}
