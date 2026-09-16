package devserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestActions() (*DevActions, *http.ServeMux) {
	cfg := &Config{
		Dev: DevConfig{
			Watch: []WatchRule{
				{Name: "go", Watch: StringOrSlice{"**/*.go"}, Cmd: "echo build"},
			},
			DockerCompose: []DockerCompose{
				{Name: "infra", File: "docker-compose.yml", Services: []string{"postgres"}},
			},
		},
	}
	pm := NewProcessManager(slog.Default())
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	es := NewErrorState()
	graph := NewGraph(cfg.Dev.Watch)
	actions := &DevActions{
		ctx: context.Background(), cfg: cfg, pm: pm, broker: broker,
		errorState: es, graph: graph, logger: slog.Default(),
		requestRun: func(*WatchRule) {},
	}
	mux := http.NewServeMux()
	actions.RegisterRoutes(mux)
	return actions, mux
}

func TestActions_RunRule(t *testing.T) {
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/rule/go/run", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])
}

// TestActions_RunRule_RoutesToScheduler verifies a manual POST /run enqueues
// the rule through requestRun (the single scheduler path) rather than starting a
// process directly — the latter would race file-watch builds and could orphan a
// process on the same port.
func TestActions_RunRule_RoutesToScheduler(t *testing.T) {
	actions, mux := newTestActions()
	var enqueued []string
	actions.requestRun = func(rule *WatchRule) { enqueued = append(enqueued, rule.Name) }

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/rule/go/run", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"go"}, enqueued, "manual run must be enqueued exactly once via the scheduler")
}

// TestActions_RunRule_NotReady verifies a manual run before the scheduler is
// wired (requestRun nil) is rejected rather than building off the scheduler.
func TestActions_RunRule_NotReady(t *testing.T) {
	actions, mux := newTestActions()
	actions.requestRun = nil

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/rule/go/run", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// TestActions_RebuildAll_EnqueuesAllRules verifies the hotkey rebuild enqueues
// every watch rule through the scheduler (which resolves topological order).
func TestActions_RebuildAll_EnqueuesAllRules(t *testing.T) {
	actions, _ := newTestActions()
	var enqueued []string
	actions.requestRun = func(rule *WatchRule) { enqueued = append(enqueued, rule.Name) }

	actions.RebuildAll()

	assert.Equal(t, []string{"go"}, enqueued)
}

func TestActions_RunRule_NotFound(t *testing.T) {
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/rule/nonexistent/run", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestActions_RunRule_InvalidPath(t *testing.T) {
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/rule/go/invalid", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestActions_DockerRestart(t *testing.T) {
	actions, mux := newTestActions()
	// Seam: record the dispatch instead of running real docker compose.
	called := make(chan string, 1)
	actions.restartFn = func(dc *DockerCompose, service string) { called <- dc.Name + "/" + service }

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/docker/infra/restart", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])

	select {
	case got := <-called:
		assert.Equal(t, "infra/", got, "whole-entry restart, no service")
	case <-time.After(2 * time.Second):
		t.Fatal("restart action was not dispatched")
	}
}

func TestActions_DockerWipe(t *testing.T) {
	actions, mux := newTestActions()
	called := make(chan string, 1)
	actions.wipeFn = func(dc *DockerCompose, service string) { called <- dc.Name + "/" + service }

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/docker/infra/wipe", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])

	select {
	case got := <-called:
		assert.Equal(t, "infra/", got)
	case <-time.After(2 * time.Second):
		t.Fatal("wipe action was not dispatched")
	}
}

func TestActions_DockerNotFound(t *testing.T) {
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/docker/unknown/restart", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestActions_DockerUnknownAction(t *testing.T) {
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/docker/infra/explode", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestActions_MethodNotAllowed(t *testing.T) {
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// GET on a POST-only endpoint.
	resp, err := http.Get(srv.URL + "/__hamr/rule/go/run")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestDockerCmd_QuotesArgs(t *testing.T) {
	// Compose file paths with spaces must not split into separate shell words.
	got := dockerCmd([]string{"compose", "-f", "/my dir/compose.yml", "up", "-d"})
	if !strings.Contains(got, `'/my dir/compose.yml'`) {
		t.Fatalf("path with space not quoted: %s", got)
	}
	// An embedded single quote is escaped, not left to break out.
	got = dockerCmd([]string{"-f", "a'b"})
	if !strings.Contains(got, `'a'\''b'`) {
		t.Fatalf("single quote not escaped: %s", got)
	}
}

// TestActions_DarkFilterToggle verifies POST /__hamr/dark flips the in-process
// dark comfort filter state (on, then back off) and reports it.
func TestActions_DarkFilterToggle(t *testing.T) {
	actions, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, want := range []bool{true, false} {
		resp, err := http.Post(srv.URL+"/__hamr/dark", "", nil)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, want, body["on"])
		assert.Equal(t, want, actions.broker.darkFilter.Load())
	}
}

// makeActions builds a DevActions wired for RunMake (pm + broker + log buffer)
// in a temp dir holding the given Makefile body.
func makeActions(t *testing.T, makefile string) (*DevActions, *LogBuffer) {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644))
	t.Chdir(dir)

	logBuf := NewLogBuffer(100)
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	pm := NewProcessManager(slog.Default())
	pm.SetLogOutput(logBuf, broker)
	return &DevActions{
		ctx: context.Background(), cfg: &Config{}, pm: pm, broker: broker,
		errorState: NewErrorState(), logger: slog.Default(), logBuf: logBuf,
	}, logBuf
}

// TestActions_RunMake verifies the whole point of routing make through
// DevActions: output reaches the shared LogBuffer (so MCP logs.read sees it no
// matter who launched the run) and the bus carries start + done events.
func TestActions_RunMake(t *testing.T) {
	actions, logBuf := makeActions(t, "hello:\n\t@echo greetings\n")
	events, cancelSub := actions.Broker().Subscribe()
	defer cancelSub()

	done, _ := actions.RunMake("hello")

	select {
	case r := <-done:
		assert.NoError(t, r.Err)
		assert.Equal(t, 0, r.ExitCode)
		assert.Contains(t, r.Output, "greetings")
	case <-time.After(20 * time.Second):
		t.Fatal("make never finished")
	}

	var texts []string
	for _, l := range logBuf.Lines() {
		assert.Equal(t, "make:hello", l.Rule)
		texts = append(texts, l.Text)
	}
	joined := strings.Join(texts, "\n")
	assert.Contains(t, joined, "greetings", "output must reach the shared log buffer, not just the caller")
	assert.Contains(t, joined, "[make:hello] exited 0", "completion marker lets an agent poll logs.read for the exit code")

	// Start is broadcast synchronously, done after the process exits, so both
	// are queued by now.
	var types []string
	for len(events) > 0 {
		types = append(types, (<-events).Type)
	}
	assert.Contains(t, types, EvMakeStart)
	assert.Contains(t, types, EvMakeDone)
}

// TestActions_RunMake_NonZeroExit checks a failing target reports its code
// rather than surfacing as a start failure.
func TestActions_RunMake_NonZeroExit(t *testing.T) {
	actions, logBuf := makeActions(t, "boom:\n\t@exit 3\n")
	// make reports its own failure code (2), not the recipe's.

	done, _ := actions.RunMake("boom")
	select {
	case r := <-done:
		assert.Error(t, r.Err)
		assert.Equal(t, 2, r.ExitCode)
	case <-time.After(20 * time.Second):
		t.Fatal("make never finished")
	}
	assert.Contains(t, lastLogText(logBuf), "[make:boom] exited 2")
}

// TestActions_RunMake_CancelKillsChildTree runs a target whose recipe
// backgrounds a grandchild holding the output pipe open. Cancelling must take
// the whole process group down — killing make alone leaves the sleep running
// and the wait blocks on the pipe forever.
func TestActions_RunMake_CancelKillsChildTree(t *testing.T) {
	actions, _ := makeActions(t, "slow:\n\tsleep 60 & sleep 60\n")

	done, cancel := actions.RunMake("slow")
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunMake did not return after cancel — child tree survived")
	}
}

func lastLogText(buf *LogBuffer) string {
	lines := buf.Lines()
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1].Text
}

// TestActions_RunMake_VisibleToLogsRead closes the loop on the bug this whole
// path exists to fix: a make run started outside MCP (the TUI's `m` hotkey
// calls RunMake exactly like this) must be readable through the gateway's
// logs.read filter, by both the exact rule and the "make" prefix.
func TestActions_RunMake_VisibleToLogsRead(t *testing.T) {
	actions, logBuf := makeActions(t, "hello:\n\t@echo greetings\n")

	done, _ := actions.RunMake("hello")
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("make never finished")
	}

	g := &mcpGateway{logBuf: logBuf}
	for _, rule := range []string{"make:hello", "make"} {
		got, err := g.logsRead([]byte(`{"rule":"` + rule + `"}`))
		require.NoError(t, err)
		entries, ok := got.([]logEntry)
		require.True(t, ok)

		var text string
		for _, e := range entries {
			text += e.Text + "\n"
		}
		assert.Contains(t, text, "greetings", "logs.read rule=%q must return the run's output", rule)
		assert.Contains(t, text, "exited 0", "logs.read rule=%q must return the completion marker", rule)
	}
}
