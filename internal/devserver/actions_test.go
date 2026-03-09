package devserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
	broker := NewSSEBroker(nil, nil, nil)
	es := NewErrorState()
	graph := NewGraph(cfg.Dev.Watch)
	actions := &DevActions{
		ctx: context.Background(), cfg: cfg, pm: pm, broker: broker,
		errorState: es, graph: graph, logger: slog.Default(),
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
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/docker/infra/restart", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])
}

func TestActions_DockerWipe(t *testing.T) {
	_, mux := newTestActions()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/__hamr/docker/infra/wipe", "", nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["ok"])
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
