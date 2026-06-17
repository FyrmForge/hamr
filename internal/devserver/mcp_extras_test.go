package devserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayLogsReadStripsANSIAndPrefixMatches(t *testing.T) {
	g := newTestGateway(t, map[string]string{"logs": "read"})
	g.logBuf.Append(LogLine{Rule: "site:build", Text: "\x1b[31mred\x1b[0m output"})
	g.enabled.Store(true)

	rec := doMCP(g, "logs.read", g.token, `{"rule":"site"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "red output", "prefix match (site→site:build) + ANSI stripped")
	assert.NotContains(t, body, "u001b", "no escape sequences in output")
	assert.NotContains(t, body, "hello world", "rule filter excludes the go line")
}

func TestConsoleSinkSnapshot(t *testing.T) {
	s := NewConsoleSink(io.Discard, false)
	s.Write(ConsoleFrame{Level: "error", Msg: "boom", Src: "app.js:1"})
	s.Write(ConsoleFrame{Level: "log", Msg: "hi"})

	all := s.Snapshot("", "", 0)
	require.Len(t, all, 2)
	assert.Equal(t, "boom", all[0].Msg)
	assert.Equal(t, "app.js:1", all[0].Src)
	assert.NotEmpty(t, all[0].Time, "frame carries a timestamp")

	errs := s.Snapshot("error", "", 0)
	require.Len(t, errs, 1)
	assert.Equal(t, "error", errs[0].Level)
}

func TestRequestLogRecords(t *testing.T) {
	rl := NewRequestLog(5)
	h := recordRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}), rl, nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/foo", nil))

	snap := rl.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, http.MethodGet, snap[0].Method)
	assert.Equal(t, "/foo", snap[0].Path)
	assert.Equal(t, http.StatusNotFound, snap[0].Status)
	assert.False(t, snap[0].Time.IsZero())
}

func TestRequestLogExcludesMCPNamespace(t *testing.T) {
	rl := NewRequestLog(5)
	h := recordRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), rl, nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/__hamr/mcp/http.read", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	snap := rl.Snapshot()
	require.Len(t, snap, 1, "the agent's own /__hamr/mcp/* calls are not recorded")
	assert.Equal(t, "/dashboard", snap[0].Path)
}

func TestRecordRequestsPreservesFlusher(t *testing.T) {
	// SSE/WS rely on the wrapped writer still being a Flusher/Hijacker.
	rl := NewRequestLog(1)
	flushed := false
	h := recordRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		require.True(t, ok, "wrapped writer must still be an http.Flusher")
		f.Flush()
		flushed = true
	}), rl, nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.True(t, flushed)
}

// deadlineRW is a base ResponseWriter that supports read deadlines, to prove
// http.ResponseController can reach it through statusRecorder.Unwrap().
type deadlineRW struct {
	http.ResponseWriter
	readDeadlineSet bool
}

func (d *deadlineRW) SetReadDeadline(time.Time) error {
	d.readDeadlineSet = true
	return nil
}

func TestStatusRecorderUnwrapReachesBaseCapabilities(t *testing.T) {
	base := &deadlineRW{ResponseWriter: httptest.NewRecorder()}
	sr := &statusRecorder{ResponseWriter: base, status: http.StatusOK}

	// statusRecorder doesn't implement SetReadDeadline; ResponseController must
	// walk Unwrap() to find it on the base (the path coder/websocket relies on).
	rc := http.NewResponseController(sr)
	require.NoError(t, rc.SetReadDeadline(time.Now().Add(time.Second)))
	assert.True(t, base.readDeadlineSet, "deadline reached the base writer via Unwrap")
}

func TestAllHealthy(t *testing.T) {
	assert.False(t, allHealthy(nil), "empty set is not healthy")
	assert.True(t, allHealthy([]containerStatus{{State: "running"}, {State: "running", Health: "healthy"}}))
	assert.False(t, allHealthy([]containerStatus{{State: "running", Health: "starting"}}))
	assert.False(t, allHealthy([]containerStatus{{State: "exited"}}))
}

func TestParseDurationOr(t *testing.T) {
	assert.Equal(t, 60*time.Second, parseDurationOr("", 60*time.Second))
	assert.Equal(t, 30*time.Second, parseDurationOr("30s", 60*time.Second))
	assert.Equal(t, 60*time.Second, parseDurationOr("garbage", 60*time.Second))
}
