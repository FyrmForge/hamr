package devserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// MCPHandshakeFile is the per-project runtime descriptor the `hamr mcp` bridge
// reads to find and authenticate to this dev server. Mode 0600, gitignored.
// Path is relative to the project root.
const MCPHandshakeFile = ".hamr/dev.json"

// mcpMaxBodyBytes bounds a single tool-call request body.
const mcpMaxBodyBytes = 1 << 20

// MCPHandshake is the JSON written to .hamr/dev.json by an enabled gateway and
// read by the `hamr mcp` bridge — the single source of truth for the wire
// format the two share.
type MCPHandshake struct {
	ProxyURL string `json:"proxyURL"`
	Token    string `json:"token"`
	PID      int    `json:"pid"`
}

// ReadMCPHandshake loads the handshake descriptor from projectRoot. Returns a
// clear error when the file is absent (dev server not running / MCP disabled).
func ReadMCPHandshake(projectRoot string) (MCPHandshake, error) {
	var hs MCPHandshake
	data, err := os.ReadFile(filepath.Join(projectRoot, MCPHandshakeFile))
	if err != nil {
		return hs, err
	}
	if err := json.Unmarshal(data, &hs); err != nil {
		return hs, fmt.Errorf("parse %s: %w", MCPHandshakeFile, err)
	}
	return hs, nil
}

// mcpGateway is the token-gated, audited HTTP surface the `hamr mcp` bridge
// drives. It mounts a single namespace, POST /__hamr/mcp/{tool}, distinct from
// the browser /__hamr/* routes — so browser traffic is never subject to token
// auth and the kill-switch gates exactly this namespace by construction.
//
// Auth is token-only (browsers never reach here). The access map is enforced
// per call via cfg.MCP.ToolAllowed — the bridge's tool-list filtering is UX,
// this is the security boundary.
type mcpGateway struct {
	cfg      *Config
	token    string
	enabled  atomic.Bool // runtime kill-switch; initial value from cfg.MCP.Enabled
	proxyURL string      // resolved (post port-walk)
	appPort  int         // resolved app port

	actions     *DevActions
	logBuf      *LogBuffer
	mailMock    *MailMock
	stripeMock  *StripeMock
	errorState  *ErrorState
	consoleSink *ConsoleSink // structured browser-console buffer, for console.read
	requestLog  *RequestLog  // proxy request ring buffer, for http.read
	makefile    string
	logger      *slog.Logger

	auditPath string       // resolved audit-log path ("" = disabled)
	audit     *mcpAuditLog // opened lazily on first activation
	logSink   func(string) // live feed for the TUI MCP tab (every request)
}

// mcpGatewayDeps groups the gateway's collaborators so the constructor stays
// readable instead of taking a dozen positional arguments.
type mcpGatewayDeps struct {
	cfg         *Config
	actions     *DevActions
	logBuf      *LogBuffer
	mailMock    *MailMock
	stripeMock  *StripeMock
	errorState  *ErrorState
	auditPath   string
	logSink     func(string)
	proxyURL    string
	appPort     int
	makefile    string
	consoleSink *ConsoleSink
	requestLog  *RequestLog
	logger      *slog.Logger
}

// newMCPGateway builds the gateway and generates its per-run token.
func newMCPGateway(d mcpGatewayDeps) (*mcpGateway, error) {
	tok, err := randomToken()
	if err != nil {
		return nil, err
	}
	g := &mcpGateway{
		cfg:         d.cfg,
		token:       tok,
		auditPath:   d.auditPath,
		logSink:     d.logSink,
		proxyURL:    d.proxyURL,
		appPort:     d.appPort,
		actions:     d.actions,
		logBuf:      d.logBuf,
		mailMock:    d.mailMock,
		stripeMock:  d.stripeMock,
		errorState:  d.errorState,
		consoleSink: d.consoleSink,
		requestLog:  d.requestLog,
		makefile:    d.makefile,
		logger:      d.logger,
	}
	return g, nil
}

// SetActive turns the gateway on or off, managing the handshake file and (on
// first activation) lazily opening the audit log. Returns nil on success.
func (g *mcpGateway) SetActive(on bool) error {
	if !on {
		g.enabled.Store(false)
		g.removeHandshake()
		return nil
	}
	if g.audit == nil {
		// Created even when the file log is disabled (auditPath == "") so the
		// TUI tab's live sink still receives every request.
		a, err := newMCPAuditLog(g.auditPath)
		if err != nil {
			return fmt.Errorf("open mcp audit log: %w", err)
		}
		a.setSink(g.logSink)
		g.audit = a // set before enabling so handlers never see a nil/partial audit
	}
	g.enabled.Store(true)
	return g.writeHandshake()
}

// Toggle flips the gateway's runtime state (the TUI kill-switch). Returns the
// new enabled state.
func (g *mcpGateway) Toggle() (bool, error) {
	on := !g.IsEnabled()
	return on, g.SetActive(on)
}

// closeAudit flushes the audit log on shutdown.
func (g *mcpGateway) closeAudit() {
	if err := g.audit.Close(); err != nil {
		g.logger.Warn("failed to close mcp audit log", "err", err)
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate mcp token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// SetEnabled flips the runtime kill-switch.
func (g *mcpGateway) SetEnabled(on bool) { g.enabled.Store(on) }

// IsEnabled reports the live gateway state.
func (g *mcpGateway) IsEnabled() bool { return g.enabled.Load() }

// EnabledToolCount returns how many tools the access map exposes, for the TUI
// indicator's "on, N tools" readout.
func (g *mcpGateway) EnabledToolCount() int { return len(g.cfg.Dev.MCP.EnabledTools()) }

// writeHandshake writes .hamr/dev.json (0600) so the bridge can find + auth.
func (g *mcpGateway) writeHandshake() error {
	hs := MCPHandshake{ProxyURL: g.proxyURL, Token: g.token, PID: os.Getpid()}
	data, err := json.Marshal(hs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(".hamr", 0o755); err != nil {
		return err
	}
	return os.WriteFile(MCPHandshakeFile, data, 0o600)
}

// removeHandshake deletes the descriptor on shutdown so a crashed/dead dev
// server doesn't leave a stale token pointing at a dead port. A missing file is
// expected (handshake never written when MCP stayed disabled); anything else is
// logged.
func (g *mcpGateway) removeHandshake() {
	if err := os.Remove(MCPHandshakeFile); err != nil && !os.IsNotExist(err) {
		g.logger.Warn("failed to remove mcp handshake file", "path", MCPHandshakeFile, "err", err)
	}
}

// RegisterRoutes mounts the gateway namespace on mux.
func (g *mcpGateway) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/__hamr/mcp/", g.handle)
}

func (g *mcpGateway) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tool := strings.TrimPrefix(r.URL.Path, "/__hamr/mcp/")

	// Token-only auth — browsers never reach this namespace. Denials are
	// audited too (a bad token, a call against a denied tool, or a call while
	// the gateway is off are exactly the events an audit log exists to record).
	if !g.authenticated(r) {
		g.audit.log(tool, "", "DENIED: unauthorized (bad or missing token)")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !g.IsEnabled() {
		g.audit.log(tool, "", "DENIED: gateway off")
		jsonError(w, "mcp gateway is off (toggle it on in the hamr dev TUI)", http.StatusForbidden)
		return
	}
	if !g.cfg.Dev.MCP.ToolAllowed(tool) {
		g.audit.log(tool, "", "DENIED: not permitted by [dev.mcp.access]")
		jsonError(w, fmt.Sprintf("tool %q not permitted by [dev.mcp.access]", tool), http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpMaxBodyBytes))
	if err != nil {
		jsonError(w, "read request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	result, err := g.dispatch(tool, body)
	if err != nil {
		g.audit.log(tool, argSummary(body), "ERROR: "+err.Error())
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	g.audit.log(tool, argSummary(body), "ok")
	// Attribute mutating actions in the main log so the developer sees agent
	// activity at a glance, right next to its effects (reads are skipped to
	// avoid noise). Routed through the dev logger (component "mcp" → a colored
	// [hamr:mcp] tag) so it lands in the TUI hamr tab and dev_logs.txt.
	if mutatingMCPTool(tool) {
		msg := tool
		if s := argSummary(body); s != "" && s != "{}" {
			msg += " " + s
		}
		g.logger.With("component", "mcp").Info(msg)
	}
	g.writeJSON(w, result)
}

// mutatingMCPTool reports whether a tool changes state (vs a pure read), so
// only writes get attributed in the main log.
func mutatingMCPTool(tool string) bool {
	switch tool {
	case "dev.info", "logs.read", "console.read", "docker.logs", "docker.status", "mail.list", "mail.get", "stripe.list":
		return false
	default:
		return true
	}
}

func (g *mcpGateway) authenticated(r *http.Request) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := strings.TrimPrefix(h, prefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(g.token)) == 1
}

// writeJSON marshals v and writes it, surfacing marshal/write failures via the
// logger rather than silently dropping them.
func (g *mcpGateway) writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		g.logger.Error("mcp: marshal response", "err", err)
		jsonError(w, "internal error encoding response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		g.logger.Warn("mcp: write response", "err", err)
	}
}

// argSummary renders a request body for the audit log: compact, single line.
func argSummary(body []byte) string {
	s := strings.TrimSpace(string(body))
	s = strings.ReplaceAll(s, "\n", " ")
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// decodeArgs unmarshals a tool's request body into v. An empty body is valid
// (tools with no required args) and leaves v at its zero value.
func decodeArgs(body []byte, v any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("invalid args: %w", err)
	}
	return nil
}

func (g *mcpGateway) dispatch(tool string, body []byte) (any, error) {
	switch tool {
	case "dev.info":
		return g.devInfo(), nil
	case "logs.read":
		return g.logsRead(body)
	case "console.read":
		return g.consoleRead(body)
	case "http.read":
		return g.httpRead(body)
	case "docker.logs":
		return g.dockerLogs(body)
	case "docker.status":
		return g.dockerStatus(body)
	case "docker.restart":
		return g.dockerAction(body, false)
	case "docker.wipe":
		return g.dockerAction(body, true)
	case "rule.run":
		return g.ruleRun(body)
	case "rebuild.all":
		g.actions.RebuildAll()
		return okResult{OK: true}, nil
	case "make.run":
		return g.makeRun(body)
	case "mail.list":
		return g.mailList(), nil
	case "mail.get":
		return g.mailGet(body)
	case "mail.clear":
		if g.mailMock != nil {
			g.mailMock.Clear()
		}
		return okResult{OK: true}, nil
	case "mail.ingest":
		return g.mailIngest(body)
	case "stripe.list":
		return g.stripeList()
	case "stripe.complete":
		return g.stripeComplete(body)
	case "stripe.expire":
		return g.stripeExpire(body)
	case "stripe.refund":
		return g.stripeRefund(body)
	default:
		return nil, fmt.Errorf("unknown tool %q", tool)
	}
}

// --- read/docker/build dispatch handlers ---

func (g *mcpGateway) logsRead(body []byte) (any, error) {
	var a logsReadArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	tail := a.Tail
	if tail <= 0 {
		tail = 200
	}
	lines := g.logBuf.Lines()
	out := make([]logEntry, 0, len(lines))
	for _, l := range lines {
		// Prefix match so rule="site" catches the "site:build"/"site:run" tags
		// that a watch rule with both a build and run step produces.
		if a.Rule != "" && l.Rule != a.Rule && !strings.HasPrefix(l.Rule, a.Rule+":") {
			continue
		}
		// Strip ANSI: the buffer holds the subprocess's raw output (escapes and
		// all); the agent wants clean text, not terminal control codes.
		text := stripANSI(l.Text)
		if a.Contains != "" && !strings.Contains(text, a.Contains) {
			continue
		}
		out = append(out, logEntry{Time: l.Time.Format(time.RFC3339), Rule: l.Rule, Text: text})
	}
	if len(out) > tail {
		out = out[len(out)-tail:]
	}
	return out, nil
}

func (g *mcpGateway) dockerLogs(body []byte) (any, error) {
	var a dockerLogsArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	dc, err := g.findStack(a.Name)
	if err != nil {
		return nil, err
	}
	if a.Service != "" && !safeServiceName.MatchString(a.Service) {
		return nil, fmt.Errorf("invalid service name")
	}
	out, err := g.actions.dockerLogsOpts(dc, a.Service, a.Tail, a.Since)
	if err != nil {
		return nil, err
	}
	if a.Contains != "" {
		out = filterLines(out, a.Contains)
	}
	return dockerLogsResult{Output: out}, nil
}

func (g *mcpGateway) dockerStatus(body []byte) (any, error) {
	var a dockerStatusArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	dc, err := g.findStack(a.Name)
	if err != nil {
		return nil, err
	}
	statuses, err := g.actions.dockerStatus(dc)
	if err != nil {
		return nil, err
	}
	if a.Service != "" {
		filtered := make([]containerStatus, 0, len(statuses))
		for _, s := range statuses {
			if s.Service == a.Service {
				filtered = append(filtered, s)
			}
		}
		statuses = filtered
	}
	return statuses, nil
}

func (g *mcpGateway) dockerAction(body []byte, wipe bool) (any, error) {
	var a dockerActionArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	dc, err := g.findStack(a.Name)
	if err != nil {
		return nil, err
	}
	if a.Service != "" && !safeServiceName.MatchString(a.Service) {
		return nil, fmt.Errorf("invalid service name")
	}

	action := g.actions.restart
	if wipe {
		action = g.actions.wipe
	}

	// Default: dispatch async, matching the browser handlers — the agent polls
	// docker.status for completion.
	if !a.Wait {
		go action(dc, a.Service)
		return okResult{OK: true}, nil
	}

	// wait: run synchronously, then poll until everything is running/healthy or
	// the timeout elapses — saves the agent a manual docker.status poll loop.
	action(dc, a.Service)
	statuses, healthy := g.waitForHealthy(dc, parseDurationOr(a.WaitTimeout, 60*time.Second))
	return dockerWaitResult{OK: true, Healthy: healthy, Statuses: statuses}, nil
}

// waitForHealthy polls docker.status until every container is running (and
// healthy, when it has a healthcheck) or the timeout elapses.
func (g *mcpGateway) waitForHealthy(dc *DockerCompose, timeout time.Duration) ([]containerStatus, bool) {
	deadline := time.Now().Add(timeout)
	for {
		statuses, err := g.actions.dockerStatus(dc)
		if err == nil && allHealthy(statuses) {
			return statuses, true
		}
		if time.Now().After(deadline) {
			return statuses, false
		}
		time.Sleep(time.Second)
	}
}

// allHealthy reports whether every container is running and, when it declares a
// healthcheck, healthy. An empty set is not healthy (nothing is up yet).
func allHealthy(ss []containerStatus) bool {
	if len(ss) == 0 {
		return false
	}
	for _, s := range ss {
		if s.State != "running" {
			return false
		}
		if s.Health != "" && s.Health != "healthy" {
			return false
		}
	}
	return true
}

// parseDurationOr parses a Go duration string, falling back to def when empty
// or invalid.
func parseDurationOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

func (g *mcpGateway) ruleRun(body []byte) (any, error) {
	var a ruleRunArgs
	if err := decodeArgs(body, &a); err != nil {
		return nil, err
	}
	for i := range g.cfg.Dev.Watch {
		if g.cfg.Dev.Watch[i].Name == a.Name {
			if g.actions.requestRun == nil {
				return nil, fmt.Errorf("dev server not ready")
			}
			g.actions.requestRun(&g.cfg.Dev.Watch[i])
			return okResult{OK: true}, nil
		}
	}
	return nil, fmt.Errorf("unknown rule %q", a.Name)
}

// findStack resolves a docker compose entry by name.
func (g *mcpGateway) findStack(name string) (*DockerCompose, error) {
	for i := range g.cfg.Dev.DockerCompose {
		if g.cfg.Dev.DockerCompose[i].Name == name {
			return &g.cfg.Dev.DockerCompose[i], nil
		}
	}
	return nil, fmt.Errorf("unknown docker stack %q", name)
}

func filterLines(s, contains string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(s, "\n") {
		if strings.Contains(line, contains) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
