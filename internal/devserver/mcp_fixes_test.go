package devserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mutatingMCPTool is derived from mcpAreas' writeTools, so reads (incl.
// http.read, which previously drifted out of the hardcoded list) are never
// attributed as mutations.
func TestMutatingMCPToolDerivedFromAreas(t *testing.T) {
	for _, read := range []string{"dev.info", "logs.read", "console.read", "http.read", "docker.logs", "docker.status", "mail.list", "mail.get", "stripe.list"} {
		assert.False(t, mutatingMCPTool(read), "%s is a read", read)
	}
	for _, write := range []string{"docker.restart", "docker.wipe", "rule.run", "rebuild.all", "make.run", "mail.clear", "mail.ingest", "stripe.complete", "stripe.expire", "stripe.refund"} {
		assert.True(t, mutatingMCPTool(write), "%s is a write", write)
	}
}

// make.run failures must read as failures in the audit log, not "ok".
func TestMakeRunResultAuditOutcome(t *testing.T) {
	zero, one := 0, 1
	assert.Equal(t, "done exit=0", makeRunResult{Status: "done", ExitCode: &zero}.auditOutcome())
	assert.Equal(t, "done exit=1", makeRunResult{Status: "done", ExitCode: &one}.auditOutcome())
	assert.Equal(t, "running", makeRunResult{Status: "running"}.auditOutcome())
}

// mail.ingest accepts a bare email string for From/To (the shape mail.list/get
// render) as well as the full object form.
func TestMailAddressUnmarshalStringOrObject(t *testing.T) {
	var s mailAddress
	require.NoError(t, json.Unmarshal([]byte(`"a@b.com"`), &s))
	assert.Equal(t, "a@b.com", s.Email)
	assert.Empty(t, s.Name)

	var o mailAddress
	require.NoError(t, json.Unmarshal([]byte(`{"Name":"X","Email":"x@y.com"}`), &o))
	assert.Equal(t, "X", o.Name)
	assert.Equal(t, "x@y.com", o.Email)

	var msg mailMessage
	require.NoError(t, json.Unmarshal([]byte(`{"From":"f@g.com","To":["t@u.com"],"Subject":"hi"}`), &msg))
	assert.Equal(t, "f@g.com", msg.From.Email)
	require.Len(t, msg.To, 1)
	assert.Equal(t, "t@u.com", msg.To[0].Email)
}

// stripe.list returns [] (not null) for empty collections, so the agent-facing
// JSON shape is stable.
func TestStripeStateSummaryEmptyMarshalsArrays(t *testing.T) {
	m := NewStripeMock(StripeMockOptions{BaseURL: "http://stripe-mock.test"})
	data, err := json.Marshal(m.stateSummary())
	require.NoError(t, err)
	for _, field := range []string{`"sessions":[]`, `"paymentIntents":[]`, `"payouts":[]`, `"refunds":[]`, `"accounts":[]`} {
		assert.Contains(t, string(data), field)
	}
	assert.NotContains(t, string(data), "null")
}

// The handshake file is written under the configured project root, not the
// process CWD — so the bridge (which resolves the project root) finds it even
// when the dev server runs from another directory.
func TestHandshakeWrittenToProjectRoot(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{Dev: DevConfig{MCP: MCPConfig{Enabled: true, Access: map[string]string{"dev": "read"}}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	g, err := newMCPGateway(mcpGatewayDeps{
		cfg:         cfg,
		projectRoot: root,
		actions:     &DevActions{cfg: cfg, logger: logger},
		logBuf:      NewLogBuffer(10),
		errorState:  NewErrorState(),
		proxyURL:    "http://localhost:3000",
		makefile:    "Makefile",
		logger:      logger,
	})
	require.NoError(t, err)

	require.NoError(t, g.SetActive(true))
	assert.FileExists(t, filepath.Join(root, MCPHandshakeFile))

	require.NoError(t, g.SetActive(false))
	assert.NoFileExists(t, filepath.Join(root, MCPHandshakeFile))
}
