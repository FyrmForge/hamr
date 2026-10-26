package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func overlayBase(w, h int, line string) string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = line
	}
	return strings.Join(rows, "\n")
}

func TestOverlayKeepsRightSide(t *testing.T) {
	const w, h = 40, 7
	line := strings.Repeat("a", 20) + strings.Repeat("Z", 20)
	out := overlay(overlayBase(w, h, line), "[mod]\n[mod]", w, h)
	rows := strings.Split(out, "\n")
	if len(rows) != h {
		t.Fatalf("rows = %d, want %d", len(rows), h)
	}
	for i, r := range rows {
		if got := lipgloss.Width(r); got != w {
			t.Errorf("row %d width = %d, want %d", i, got, w)
		}
	}
	// modal is 5 wide at startX 17..22: right side must keep Z's.
	if want := strings.Repeat("a", 17) + "[mod]" + strings.Repeat("Z", 18); !strings.Contains(ansi.Strip(rows[3]), want) {
		t.Errorf("row 3 = %q, want it to contain %q", ansi.Strip(rows[3]), want)
	}
}

func TestOverlayStyledAndWide(t *testing.T) {
	const w, h = 30, 5
	styled := "\x1b[31m" + strings.Repeat("r", 14) + "\x1b[0m\x1b[32m" + strings.Repeat("g", 16) + "\x1b[0m"
	out := overlay(overlayBase(w, h, styled), "MODAL", w, h)
	for i, r := range strings.Split(out, "\n") {
		if lipgloss.Width(r) != w {
			t.Errorf("styled row %d width = %d", i, lipgloss.Width(r))
		}
	}
	if !strings.Contains(out, strings.Repeat("g", 10)) {
		t.Errorf("right side lost: %q", out)
	}

	// wide runes straddling both cut points
	wide := strings.Repeat("日", 15)
	out = overlay(overlayBase(w, h, wide), "MODAL", w, h)
	for i, r := range strings.Split(out, "\n") {
		if lipgloss.Width(r) != w {
			t.Errorf("wide row %d width = %d", i, lipgloss.Width(r))
		}
	}
}

func TestOverlayShortBasePads(t *testing.T) {
	const w, h = 30, 5
	out := overlay(overlayBase(w, h, "hi"), "MODAL", w, h)
	for i, r := range strings.Split(out, "\n") {
		if lipgloss.Width(r) != w && i == 2 {
			t.Errorf("row %d width = %d, want %d", i, lipgloss.Width(r), w)
		}
	}
	if !strings.Contains(ansi.Strip(out), "hi") {
		t.Error("short base text lost")
	}
}
