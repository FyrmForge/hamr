package devserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrefixWriter_FlushEmitsTrailingPartialLine(t *testing.T) {
	var buf bytes.Buffer
	pw := newPrefixWriter(&buf, "site", "")

	// A complete line is written through immediately.
	_, _ = pw.Write([]byte("first line\n"))
	// A trailing line WITHOUT a newline (e.g. a crash message) is held...
	_, _ = pw.Write([]byte("panic: boom"))
	if strings.Contains(buf.String(), "boom") {
		t.Fatal("trailing partial line should be buffered until Flush")
	}

	// ...and only emitted on Flush.
	pw.Flush()
	if !strings.Contains(buf.String(), "panic: boom") {
		t.Fatalf("Flush must emit the trailing line; got %q", buf.String())
	}
	// Flush is idempotent (no duplicate emission).
	before := buf.Len()
	pw.Flush()
	if buf.Len() != before {
		t.Fatal("second Flush must not re-emit")
	}
}
