// Package logging provides context-aware structured logging wrapping log/slog.
//
// In production mode a JSON handler is used; in development mode a coloured
// tint handler makes output easy to scan. Loggers are threaded through
// context.Context so that request-scoped attributes propagate automatically.
package logging

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"
)

type ctxKey struct{}

// New creates a new *slog.Logger. When production is true a JSON handler
// writing to stdout is used; otherwise a tint handler with coloured output and
// timestamps is returned.
func New(production bool) *slog.Logger {
	if production {
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
	}
	return slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:      slog.LevelDebug,
		TimeFormat: time.Kitchen,
	}))
}

// FromContext extracts the logger stored in ctx. If no logger is found the
// package-level default logger is returned.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithLogger returns a copy of ctx carrying l.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// With returns a copy of ctx whose logger has the supplied slog attributes
// appended. If ctx does not carry a logger the default logger is used.
func With(ctx context.Context, args ...any) context.Context {
	return WithLogger(ctx, FromContext(ctx).With(args...))
}
