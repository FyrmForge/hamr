package devserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	broker := NewSSEBroker(nil, nil, nil, false, false, false)
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
