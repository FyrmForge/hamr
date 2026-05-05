package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	ws "github.com/coder/websocket"
)

// siteConsoleTag is the colored prefix applied to every browser-console line
// that lands in the dev TUI / log file. Cyan-bold to match [hamr dev], with
// a different label so the source is unambiguous at a glance.
var siteConsoleTag = []byte(tagColor + "[site:console]" + colorReset + " ")

// siteLevelColors maps incoming JS levels to the same ANSI palette the
// backend slog handler uses for warn/error.
var siteLevelColors = map[string]string{
	"warn":  "\033[33m",
	"error": "\033[31m",
}

// ConsoleFrame is one log entry sent up by the browser. The field set is
// kept narrow on purpose: anything more (timestamps, URLs, multi-tab IDs)
// goes through a different conversation.
type ConsoleFrame struct {
	// Level is one of: "log", "info", "debug", "warn", "error".
	// Internal categories the JS may send for non-console events:
	// "rejection" (unhandled promise), "resource" (load failure),
	// "csp" (CSP violation). Only "warn" and "error" get a colored
	// uppercase level label in the rendered line; every other value
	// (including the internal categories) renders without a label so
	// the line stays scannable. This matches the backend slog handler's
	// convention.
	Level string `json:"level"`

	// Msg is the rendered text (args joined client-side, objects already
	// JSON-stringified, capped per-arg by the JS serializer).
	Msg string `json:"msg"`

	// Src is an optional source location, used only for uncaught errors
	// where the file:line:col is load-bearing. Plain console.* calls
	// leave it empty.
	Src string `json:"src,omitempty"`
}

// ConsoleSink is the dev-server side of the browser-console transport. It
// owns the WS endpoint and a single io.Writer (the same multiwriter the
// dev slog handler uses) so frames land in TUI tab 0 and the rolling
// dev_logs.txt file alongside backend events, in arrival order.
type ConsoleSink struct {
	w          io.Writer
	filterHamr bool
	mu         sync.Mutex
}

// NewConsoleSink wires the sink to the same writer as the dev logger.
// Pass filterHamr=true to drop frames whose msg contains "[hamr]" (i.e.
// hamr's own reload-script chatter); default is to show everything.
func NewConsoleSink(w io.Writer, filterHamr bool) *ConsoleSink {
	return &ConsoleSink{w: w, filterHamr: filterHamr}
}

// Write renders a single frame and emits it through the dev writer. Empty
// messages and (when filtering) hamr-prefixed messages are dropped.
// Exported so tests can drive formatting without standing up a WS server.
func (c *ConsoleSink) Write(f ConsoleFrame) {
	if f.Msg == "" {
		return
	}
	if c.filterHamr && strings.Contains(f.Msg, "[hamr]") {
		return
	}

	var buf []byte
	buf = append(buf, siteConsoleTag...)
	if lc, ok := siteLevelColors[f.Level]; ok {
		buf = append(buf, lc...)
		buf = append(buf, strings.ToUpper(f.Level)...)
		buf = append(buf, colorReset...)
		buf = append(buf, ' ')
	}
	buf = append(buf, f.Msg...)
	if f.Src != "" {
		buf = append(buf, " @ "...)
		buf = append(buf, f.Src...)
	}
	buf = append(buf, '\r', '\n')

	// Match the devHandler write protocol: termMu serializes against
	// every other terminal source (slog, prefixWriter, status bar) so a
	// burst of browser frames doesn't tear backend output mid-line.
	termMu.Lock()
	c.mu.Lock()
	_, _ = c.w.Write(buf)
	c.mu.Unlock()
	termMu.Unlock()
}

// Handler returns the WS upgrade handler. Mount at /__hamr/console. Each
// frame on the wire is JSON; the wire format accepts either a single
// ConsoleFrame object or an array (the JS client batches small bursts to
// keep frame count down). Unparseable payloads are dropped silently —
// dev-only, not worth surfacing to the user.
//
// Origin gating is the coder/websocket default: Origin must equal Host.
// In dev that's always true (the JS is injected by the same proxy that
// serves the WS). External callers hitting the endpoint cross-origin
// will be rejected at upgrade.
func (c *ConsoleSink) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow() //nolint:errcheck

		ctx := r.Context()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				// Normal close, network drop, or peer hangup — all are
				// expected in the dev loop (page navigation, etc.).
				return
			}
			c.ingest(data)
		}
	})
}

// ingest parses a wire payload and forwards each frame to Write. Accepts
// either a single object or an array; anything else is ignored.
func (c *ConsoleSink) ingest(data []byte) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return
	}
	if trimmed[0] == '[' {
		var batch []ConsoleFrame
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return
		}
		for _, f := range batch {
			c.Write(f)
		}
		return
	}
	var single ConsoleFrame
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return
	}
	c.Write(single)
}
