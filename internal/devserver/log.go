package devserver

import (
	"context"
	"io"
	"log/slog"
	"sync"
)

// ANSI styles for the [hamr dev] tag.
const (
	tagColor = "\033[1;97m" // bold bright white
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
type devHandler struct {
	w     io.Writer
	mu    *sync.Mutex
	level slog.Level
	attrs []slog.Attr
}

func newDevHandler(w io.Writer, level slog.Level) *devHandler {
	return &devHandler{w: w, mu: &sync.Mutex{}, level: level}
}

func (h *devHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *devHandler) Handle(_ context.Context, r slog.Record) error {
	var buf []byte

	// Tag.
	buf = append(buf, hamrTag...)

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
		writeAttr(a)
		return true
	})

	buf = append(buf, '\n')

	h.mu.Lock()
	_, err := h.w.Write(buf)
	h.mu.Unlock()
	return err
}

func (h *devHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &devHandler{w: h.w, mu: h.mu, level: h.level, attrs: newAttrs}
}

func (h *devHandler) WithGroup(name string) slog.Handler {
	// Groups not used in the dev server — treat as prefix.
	newAttrs := make([]slog.Attr, len(h.attrs))
	copy(newAttrs, h.attrs)
	return &devHandler{w: h.w, mu: h.mu, level: h.level, attrs: newAttrs}
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
