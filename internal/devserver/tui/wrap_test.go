package tui

import (
	"strings"
	"testing"
)

func TestHardwrap_ShortLine(t *testing.T) {
	got := hardwrap("hello", 10)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("expected single chunk, got %v", got)
	}
}

func TestHardwrap_BreaksAtWidth(t *testing.T) {
	got := hardwrap("abcdefghij", 4)
	want := []string{"abcd\x1b[0m", "efgh\x1b[0m", "ij"}
	if len(got) != len(want) {
		t.Fatalf("expected %d chunks, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestHardwrap_IgnoresCSI(t *testing.T) {
	// Visible text "abcdef" with a colour escape in the middle.
	in := "ab\x1b[31mcd\x1b[0mef"
	got := hardwrap(in, 4)
	// Width counts only visible runes: 4 cols → split after "abcd".
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d (%v)", len(got), got)
	}
	if !strings.Contains(got[0], "ab\x1b[31mcd") {
		t.Fatalf("first chunk lost ANSI: %q", got[0])
	}
	if !strings.HasSuffix(got[0], "\x1b[0m") {
		t.Fatalf("first chunk should be reset-terminated: %q", got[0])
	}
	if !strings.Contains(got[1], "ef") {
		t.Fatalf("second chunk missing tail: %q", got[1])
	}
}

func TestHardwrap_ZeroWidth(t *testing.T) {
	got := hardwrap("anything", 0)
	if len(got) != 1 || got[0] != "anything" {
		t.Fatalf("width 0 should pass through, got %v", got)
	}
}

func TestWrapForView_PreservesSeparateLines(t *testing.T) {
	in := "abcd\nefgh"
	got := wrapForView(in, 4)
	if got != "abcd\nefgh" {
		t.Fatalf("got %q want %q", got, "abcd\nefgh")
	}
}

func TestWrapForView_WrapsLongLines(t *testing.T) {
	in := "abcdefgh\nij"
	got := wrapForView(in, 4)
	want := "abcd\x1b[0m\nefgh\nij"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWrapForView_EmptyAndZeroWidth(t *testing.T) {
	if got := wrapForView("", 80); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	if got := wrapForView("xy", 0); got != "xy" {
		t.Fatalf("zero width: got %q", got)
	}
}
