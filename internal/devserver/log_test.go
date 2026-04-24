package devserver

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stripANSI is defined in errorpage.go.

// newTestLogger returns a logger writing into buf with the dev handler at
// info level. Helper so tests stay focused on output assertions.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(newDevHandler(buf, slog.LevelInfo))
}

func TestDevHandler_DefaultTag(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).Info("hello", "k", "v")

	plain := stripANSI(buf.String())
	assert.Contains(t, plain, "[hamr dev] ", "default prefix is the [hamr dev] tag")
	assert.Contains(t, plain, "hello", "message is rendered")
	assert.Contains(t, plain, "k=v", "attrs render as key=value")
	assert.NotContains(t, plain, "[hamr:", "no component prefix when none was set")
}

func TestDevHandler_ComponentTag(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).With("component", "stripe").Info("mock enabled", "port", 3000)

	plain := stripANSI(buf.String())
	assert.Contains(t, plain, "[hamr:stripe] ", "component swaps the prefix")
	assert.NotContains(t, plain, "[hamr dev]", "default prefix is replaced, not duplicated")
	assert.NotContains(t, plain, "component=", "component attr is consumed for the prefix, not rendered")
	assert.Contains(t, plain, "mock enabled", "message survives")
	assert.Contains(t, plain, "port=3000", "non-component attrs survive")
}

func TestDevHandler_ComponentChainedWithAttrs(t *testing.T) {
	// Regression guard: component must persist across additional .With(...)
	// calls. Without this, only the very last .With chain would carry the
	// tag and any pre-tag attrs would silently lose their prefix.
	var buf bytes.Buffer
	newTestLogger(&buf).
		With("component", "stripe").
		With("foo", "bar").
		Info("layered")

	plain := stripANSI(buf.String())
	assert.Contains(t, plain, "[hamr:stripe] ", "component survives chained With calls")
	assert.Contains(t, plain, "foo=bar", "later With attrs are appended")
	assert.Contains(t, plain, "layered", "message survives")
}

func TestDevHandler_PerCallComponentDropped(t *testing.T) {
	// A `component` key passed at the call site (not via .With) is not a
	// real attr — Handle drops it. This prevents callers from accidentally
	// leaking `component=stripe` into the rendered key=value list.
	var buf bytes.Buffer
	newTestLogger(&buf).Info("ad-hoc", "component", "stripe", "k", "v")

	plain := stripANSI(buf.String())
	assert.NotContains(t, plain, "component=", "per-call component is filtered out at Handle time")
	assert.Contains(t, plain, "k=v", "other per-call attrs survive")
	// Per-call component does NOT change the tag — only WithAttrs does. The
	// rendered prefix is still the default [hamr dev].
	assert.Contains(t, plain, "[hamr dev] ", "per-call component does not retroactively change the prefix")
}

func TestDevHandler_NonStringComponentRendersAsAttr(t *testing.T) {
	// WithAttrs only consumes `component` when the value is a string;
	// other types fall through and render as normal attrs. Documents the
	// type-strict behaviour so a future refactor doesn't unintentionally
	// widen it to consume any component value.
	var buf bytes.Buffer
	newTestLogger(&buf).With("component", 42).Info("typed")

	plain := stripANSI(buf.String())
	assert.Contains(t, plain, "[hamr dev] ", "non-string component does not change the prefix")
	assert.Contains(t, plain, "component=42", "non-string component is rendered as a normal attr")
}

func TestDevHandler_ColorCodesPresent(t *testing.T) {
	// Smoke test the ANSI styling so a future refactor that drops the
	// coloring (e.g. replacing the writer with one that strips it) is
	// caught by tests rather than only noticed in a terminal.
	var buf bytes.Buffer
	newTestLogger(&buf).With("component", "stripe").Info("colored")

	raw := buf.String()
	assert.Contains(t, raw, tagColor, "tag color escape is emitted")
	assert.Contains(t, raw, colorReset, "color reset escape closes the tag styling")
}

func TestDevHandler_LevelLabelOnWarnAndError(t *testing.T) {
	// Warn/Error get a level label inserted between tag and message;
	// Info/Debug don't. This is what gives `[hamr dev] WARN something`
	// its visual weight in a busy log stream.
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	logger.Info("info-msg")
	plain := stripANSI(buf.String())
	assert.NotContains(t, plain, "INFO", "info level has no inline label")
	assert.Contains(t, plain, "info-msg")

	buf.Reset()
	logger.Warn("warn-msg")
	plain = stripANSI(buf.String())
	assert.Contains(t, plain, "WARN ", "warn level renders an inline label")
	assert.Contains(t, plain, "warn-msg")

	buf.Reset()
	logger.Error("err-msg")
	plain = stripANSI(buf.String())
	assert.Contains(t, plain, "ERROR ", "error level renders an inline label")
	assert.Contains(t, plain, "err-msg")
}

func TestDevHandler_LineEnding(t *testing.T) {
	// Each record terminates with \r\n (CRLF). Important when the logger
	// writes to a TTY shared with other output (e.g. spawned subprocess
	// stdout): a bare \n could leave the cursor mid-line on some terminals.
	var buf bytes.Buffer
	newTestLogger(&buf).Info("eol")

	require.True(t, strings.HasSuffix(buf.String(), "\r\n"), "every record ends with CRLF")
}

func TestDevHandler_LevelGate(t *testing.T) {
	// Logs below the handler's level must not appear at all — the handler
	// is the gate, since slog.Logger only checks Enabled before formatting.
	var buf bytes.Buffer
	logger := slog.New(newDevHandler(&buf, slog.LevelWarn))

	logger.Info("hidden")
	logger.Debug("also-hidden")
	assert.Empty(t, buf.String(), "info+debug suppressed when handler level is warn")

	logger.Warn("shown")
	assert.Contains(t, stripANSI(buf.String()), "shown")
}
