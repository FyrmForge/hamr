package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestModel_Bars_TruncateOverflowingLeftCluster guards the bar-overflow bug:
// when the left cluster alone (long ERR list, search query) exceeds the
// terminal width, the bar must truncate it rather than overflow and wrap — a
// wrapped bar pushes every row down and corrupts the whole frame.
func TestModel_Bars_TruncateOverflowingLeftCluster(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.width = 30

	// Status bar: a long ERR rule list overflows the width.
	m.errors = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}
	if w := lipgloss.Width(m.statusBar()); w > m.width {
		t.Fatalf("status bar width %d exceeds terminal width %d", w, m.width)
	}
	m.errors = nil

	// Hint bar: an active search with a long query overflows the left cluster.
	s := m.activeSearch()
	s.open()
	for _, r := range strings.Repeat("q", 80) {
		s.appendRune(r)
	}
	s.commit(m.currentLogs())
	if w := lipgloss.Width(m.hintBar()); w > m.width {
		t.Fatalf("hint bar width %d exceeds terminal width %d", w, m.width)
	}
}

// TestModel_HelpModal_FitsAvailableHeight guards the modal off-by-one: at tight
// terminal heights the rendered help modal must not exceed the available rows
// (m.height-2), or its bottom border is cropped where it overlaps the hint bar.
func TestModel_HelpModal_FitsAvailableHeight(t *testing.T) {
	// h>=12 is the range where the entry-budget logic is meant to size the
	// modal to fit; the uncounted title margin pushes it one row over.
	for h := 12; h <= 24; h++ {
		m := NewModel(NewHotkeySource())
		m.width = 60
		m.height = h
		got := lipgloss.Height(m.helpView())
		if avail := m.availableModalHeight(); got > avail {
			t.Fatalf("height=%d: help modal renders %d rows, exceeds available %d (bottom border cropped)", h, got, avail)
		}
	}
}
