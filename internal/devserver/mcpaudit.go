package devserver

import (
	"fmt"
	"sync"
	"time"
)

// mcpAuditLogMaxLines caps the rolling MCP audit log. Generous vs the dev log
// (audit lines are one-per-call and worth keeping more history of).
const mcpAuditLogMaxLines = 2000

// mcpAuditLog is the append-only record of agent tool calls, written
// server-side at the gateway's auth choke point so it's complete regardless of
// bridge behavior. Mirrors the dev log file mechanism (rollingFileWriter) and
// feeds the dedicated MCP TUI tab.
type mcpAuditLog struct {
	mu  sync.Mutex
	w   *rollingFileWriter // nil when file logging is disabled (log_file = "none")
	now func() time.Time
	// sink, when set, also receives each rendered line so the TUI MCP tab can
	// render the request stream live — independent of the file writer.
	sink func(string)
}

// newMCPAuditLog builds the audit log. When path is "" the file writer is
// disabled but the struct is still returned so a live sink (the TUI tab) can be
// attached.
func newMCPAuditLog(path string) (*mcpAuditLog, error) {
	l := &mcpAuditLog{now: time.Now}
	if path != "" {
		w, err := newRollingFileWriter(path, mcpAuditLogMaxLines)
		if err != nil {
			return nil, err
		}
		l.w = w
	}
	return l, nil
}

// setSink registers a live consumer for rendered audit lines (the TUI MCP tab).
func (l *mcpAuditLog) setSink(fn func(string)) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.sink = fn
	l.mu.Unlock()
}

// log records one tool call. tool is the tool name, args a compact rendering of
// the key arguments, outcome the result summary (e.g. "ok", "done exit=0",
// "ERROR: ..."). Safe to call on a nil receiver (audit disabled).
func (l *mcpAuditLog) log(tool, args, outcome string) {
	if l == nil {
		return
	}
	ts := l.now().UTC().Format(time.RFC3339)
	body := tool
	if args != "" {
		body += " " + args
	}
	line := fmt.Sprintf("%s [mcp] %s → %s", ts, body, outcome)
	l.mu.Lock()
	if l.w != nil {
		_, _ = l.w.Write([]byte(line + "\n"))
	}
	sink := l.sink
	l.mu.Unlock()
	if sink != nil {
		sink(line)
	}
}

// Close flushes and closes the underlying file. Safe on a nil receiver.
func (l *mcpAuditLog) Close() error {
	if l == nil || l.w == nil {
		return nil
	}
	return l.w.Close()
}
