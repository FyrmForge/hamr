package tui

import (
	"errors"
	"testing"
)

type errWriter struct {
	err   error
	calls int
}

func (e *errWriter) Write(p []byte) (int, error) {
	e.calls++
	return 0, e.err
}

// TestMakePrefixWriter_ReturnsFullLenOnDownstreamError guards the io.Writer
// contract violation: Write appends all of p to its internal buffer, so on a
// downstream error it must report len(p) consumed — returning 0 would invite a
// retry of the same bytes, duplicating the line.
func TestMakePrefixWriter_ReturnsFullLenOnDownstreamError(t *testing.T) {
	ew := &errWriter{err: errors.New("sink closed")}
	w := newMakePrefixWriter(ew, "build", "")

	p := []byte("hello\n")
	n, err := w.Write(p)

	if err == nil {
		t.Fatal("expected the downstream error to propagate")
	}
	if n != len(p) {
		t.Fatalf("Write must report all %d bytes consumed (else caller retries and dups), got %d", len(p), n)
	}
}
