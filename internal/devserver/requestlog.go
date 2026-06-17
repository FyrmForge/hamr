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
	entries []RequestLogEntry
	max     int
}

// NewRequestLog creates a request log capped at max entries.
func NewRequestLog(max int) *RequestLog {
	return &RequestLog{entries: make([]RequestLogEntry, 0, max), max: max}
}

// Record appends an entry, trimming the oldest once over capacity.
func (rl *RequestLog) Record(e RequestLogEntry) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
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
	copy(out, rl.entries)
	return out
}

// recordRequests wraps next so each served request is recorded into rl after it
// completes (the final status is known then).
func recordRequests(next http.Handler, rl *RequestLog) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		// Don't record the agent's own MCP tool calls — http.read would then
		// log itself recursively, drowning the real app traffic.
		if strings.HasPrefix(r.URL.Path, "/__hamr/mcp/") {
			return
		}
		rl.Record(RequestLogEntry{
			Time:       start,
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     sr.status,
			DurationMs: time.Since(start).Milliseconds(),
		})
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
