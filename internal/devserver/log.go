package devserver

import (
	"context"
	"io"
	"log/slog"
)

// ANSI styles for the [hamr dev] tag.
const (
	tagColor = "\033[1;36m" // bold cyan
)

var hamrTag = []byte(tagColor + "[hamr dev]" + colorReset + " ")

// levelColors maps slog levels to ANSI colors.
var levelColors = map[slog.Level]string{
	slog.LevelDebug: "\033[90m",  // gray
	slog.LevelInfo:  "\033[37m",  // white (default)
	slog.LevelWarn:  "\033[33m",  // yellow
	slog.LevelError: "\033[31m",  // red
}

// devHandler is a compact slog.Handler that writes lines prefixed with
// a colored [hamr dev] tag so they stand out from child process output.
//
// Format: [hamr dev] message key=value key=value ...
// Errors and warnings get colored level labels.
//
// Subcomponent prefix: a logger derived via `.With("component", "stripe")`
// (or any component string) renders as `[hamr:stripe]` instead of
// `[hamr dev]`. The component attr is consumed for the prefix and not
// duplicated in the rendered attr list.
type devHandler struct {
	w         io.Writer
	level     slog.Level
	attrs     []slog.Attr
	component string // empty = use default [hamr dev] tag
}

func newDevHandler(w io.Writer, level slog.Level) *devHandler {
	return &devHandler{w: w, level: level}
}

func (h *devHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *devHandler) Handle(_ context.Context, r slog.Record) error {
	var buf []byte

	// Tag — default is [hamr dev], but components get [hamr:<component>].
	if h.component != "" {
		buf = append(buf, tagColor...)
		buf = append(buf, '[', 'h', 'a', 'm', 'r', ':')
		buf = append(buf, h.component...)
		buf = append(buf, ']')
		buf = append(buf, colorReset...)
		buf = append(buf, ' ')
	} else {
		buf = append(buf, hamrTag...)
	}

	// Level label for warn/error.
	lc, ok := levelColors[r.Level]
	if !ok {
		lc = levelColors[slog.LevelInfo]
	}

	if r.Level >= slog.LevelWarn {
		buf = append(buf, lc...)
		buf = append(buf, r.Level.String()...)
		buf = append(buf, colorReset...)
		buf = append(buf, ' ')
	}

	// Message.
	buf = append(buf, r.Message...)

	// Attrs (pre-set + record).
	writeAttr := func(a slog.Attr) {
		if a.Equal(slog.Attr{}) {
			return
		}
		buf = append(buf, ' ')
		buf = append(buf, a.Key...)
		buf = append(buf, '=')
		buf = append(buf, a.Value.String()...)
	}

	for _, a := range h.attrs {
		writeAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		// Skip per-call "component" — it's a prefix override, not data.
		// Handler-level component was already applied above.
		if a.Key == "component" {
			return true
		}
		writeAttr(a)
		return true
	})

	buf = append(buf, '\r', '\n')

	termMu.Lock()
	_, err := h.w.Write(buf)
	termMu.Unlock()
	return err
}

func (h *devHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newComp := h.component
	kept := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	kept = append(kept, h.attrs...)
	// Intercept "component" — use it for the prefix, drop from rendered
	// attrs. Per-call attrs (slog.Logger.Info("...", "component", "x"))
	// are filtered the same way at Handle time.
	for _, a := range attrs {
		if a.Key == "component" && a.Value.Kind() == slog.KindString {
			newComp = a.Value.String()
			continue
		}
		kept = append(kept, a)
	}
	return &devHandler{w: h.w, level: h.level, attrs: kept, component: newComp}
}

func (h *devHandler) WithGroup(name string) slog.Handler {
	// Groups not used in the dev server — treat as prefix.
	newAttrs := make([]slog.Attr, len(h.attrs))
	copy(newAttrs, h.attrs)
	return &devHandler{w: h.w, level: h.level, attrs: newAttrs, component: h.component}
}

var _ slog.Handler = (*devHandler)(nil)

// newDevLogger creates a pretty logger for hamr dev with a colored [hamr dev] prefix.
func newDevLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(newDevHandler(w, level))
}

// HamrDevTag returns the colored [hamr dev] tag string for use outside the logger.
func HamrDevTag() string {
	return string(hamrTag)
}
