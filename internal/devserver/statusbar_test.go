package devserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestStatusBarWriteDrawSequenceClearsPreviousBarRowOnResize(t *testing.T) {
	var sb StatusBar
	var buf bytes.Buffer

	sb.writeDrawSequence(&buf, 80, 30, 24)
	out := buf.String()

	if !strings.HasPrefix(out, "\033[r") {
		t.Fatalf("draw sequence should start by resetting the scroll region: %q", out)
	}

	if !strings.Contains(out, "\033[24;1H\033[2K") {
		t.Fatalf("draw sequence should clear the previous bar row after resize: %q", out)
	}

	if !strings.Contains(out, "\033[1;29r") {
		t.Fatalf("draw sequence should reserve the new bottom row: %q", out)
	}

	if !strings.Contains(out, "\033[30;1H\033[2K") {
		t.Fatalf("draw sequence should clear the new bar row: %q", out)
	}

	if !strings.HasSuffix(out, "\033[29;1H") {
		t.Fatalf("draw sequence should leave the cursor above the bar: %q", out)
	}
}

func TestStatusBarWriteDrawSequenceSkipsPreviousRowClearWhenHeightUnchanged(t *testing.T) {
	var sb StatusBar
	var buf bytes.Buffer

	sb.writeDrawSequence(&buf, 80, 24, 24)
	out := buf.String()

	if strings.Count(out, "\033[24;1H\033[2K") != 1 {
		t.Fatalf("draw sequence should only clear the active bar row when height is unchanged: %q", out)
	}
}
