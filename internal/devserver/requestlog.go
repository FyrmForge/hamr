package devserver

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RequestLogEntry is one observed HTTP request through the dev proxy.
type RequestLogEntry struct {
	Time       time.Time `json:"time"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"durationMs"`
}

// RequestLog is a thread-safe ring buffer of recent proxy requests, feeding the
// MCP http.read tool. It captures every request the proxy serves — proxied app
// traffic, static assets, the /__hamr/* endpoints, and the SSE/WS streams —
// which is the view the app's own access log can't give (the app never sees
// proxy-handled routes, and skips /static).
type RequestLog struct {
	mu      sync.Mutex
	entries []*RequestLogEntry // pointers so an in-flight entry can be finalized after eviction
	max     int
}

// NewRequestLog creates a request log capped at max entries.
func NewRequestLog(max int) *RequestLog {
	return &RequestLog{entries: make([]*RequestLogEntry, 0, max), max: max}
}

// Record appends a completed entry, trimming the oldest once over capacity.
func (rl *RequestLog) Record(e RequestLogEntry) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.push(&e)
}

// begin appends an in-flight entry (Status/DurationMs zero) so http.read sees
// the request immediately — important for long-lived SSE/WS connections that
// would otherwise stay invisible until they close. Caller finalizes on
// completion. Returns the entry to pass to finalize.
func (rl *RequestLog) begin(method, path string, t time.Time) *RequestLogEntry {
	e := &RequestLogEntry{Time: t, Method: method, Path: path}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.push(e)
	return e
}

// finalize records an in-flight entry's final status and duration under the
// lock (Snapshot may read it concurrently). The entry may already have been
// evicted from the ring by then; mutating it is harmless.
func (rl *RequestLog) finalize(e *RequestLogEntry, status int, dur time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e.Status = status
	e.DurationMs = dur.Milliseconds()
}

// push appends and trims; caller holds rl.mu.
func (rl *RequestLog) push(e *RequestLogEntry) {
	rl.entries = append(rl.entries, e)
	if len(rl.entries) > rl.max {
		rl.entries = rl.entries[len(rl.entries)-rl.max:]
	}
}

// Snapshot returns a copy of all buffered entries (oldest first).
func (rl *RequestLog) Snapshot() []RequestLogEntry {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	out := make([]RequestLogEntry, len(rl.entries))
	for i, e := range rl.entries {
		out[i] = *e
	}
	return out
}

// recordRequests wraps next so each served request is recorded into rl. The
// entry is appended at request start (so in-flight SSE/WS are visible to
// http.read) and finalized with status/duration on completion. Recording is
// skipped — at near-zero overhead — for the agent's own /__hamr/mcp/* calls
// (http.read would log itself) and when the gateway is off (nothing reads the
// ring until MCP is enabled). gw nil means always record (used in tests).
func recordRequests(next http.Handler, rl *RequestLog, gw *mcpGateway) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__hamr/mcp/") || (gw != nil && !gw.IsEnabled()) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		entry := rl.begin(r.Method, r.URL.Path, start)
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		rl.finalize(entry, sr.status, time.Since(start))
	})
}

// statusRecorder captures the response status while preserving the streaming
// interfaces the proxy relies on — Flusher for SSE live-reload and Hijacker for
// the browser-console WebSocket. Without these pass-throughs, wrapping the
// ResponseWriter would silently break those long-lived connections.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the base writer for any capability
// the recorder doesn't implement directly (read/write deadlines, etc.) — the
// idiomatic way to wrap a ResponseWriter without hiding the streaming
// interfaces the proxy's SSE/WS endpoints rely on.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("response writer does not support hijacking")
}
