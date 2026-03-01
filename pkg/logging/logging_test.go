package logging_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/FyrmForge/hamr/pkg/logging"
)

func TestNew_production(t *testing.T) {
	l := logging.New(true)
	if l == nil {
		t.Fatal("New(true) returned nil")
	}
}

func TestNew_development(t *testing.T) {
	l := logging.New(false)
	if l == nil {
		t.Fatal("New(false) returned nil")
	}
}

func TestFromContext_empty(t *testing.T) {
	l := logging.FromContext(context.Background())
	if l == nil {
		t.Fatal("FromContext on empty context returned nil")
	}
}

func TestWithLogger_roundtrip(t *testing.T) {
	l := slog.Default()
	ctx := logging.WithLogger(context.Background(), l)
	got := logging.FromContext(ctx)
	if got != l {
		t.Fatal("FromContext did not return the logger stored by WithLogger")
	}
}

func TestWith(t *testing.T) {
	ctx := context.Background()
	ctx = logging.WithLogger(ctx, logging.New(false))
	ctx = logging.With(ctx, "request_id", "abc123")

	// Ensure the returned context still carries a logger.
	l := logging.FromContext(ctx)
	if l == nil {
		t.Fatal("With returned context without logger")
	}
}
