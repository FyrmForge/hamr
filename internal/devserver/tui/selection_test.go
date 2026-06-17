package tui

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func TestSelection_ClickPlainReplaces(t *testing.T) {
	s := &selectionState{}
	s.clickPlain(3)
	s.clickPlain(7)
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{7}) {
		t.Fatalf("plain click should replace; got %v", got)
	}
	if s.anchor != 7 {
		t.Fatalf("anchor should follow plain click, got %d", s.anchor)
	}
}

func TestSelection_ClickToggleAddsAndRemoves(t *testing.T) {
	s := &selectionState{}
	s.clickToggle(2)
	s.clickToggle(5)
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{2, 5}) {
		t.Fatalf("toggle should add both, got %v", got)
	}
	s.clickToggle(2)
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{5}) {
		t.Fatalf("toggle on selected should remove, got %v", got)
	}
}

func TestSelection_ClickRangeFromAnchor(t *testing.T) {
	s := &selectionState{}
	s.clickPlain(3)
	s.clickRange(7, nil)
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{3, 4, 5, 6, 7}) {
		t.Fatalf("range from anchor 3 to 7 wrong: %v", got)
	}
	// Anchor must be unchanged so a second shift-click extends from
	// the same origin, not the most recent endpoint.
	if s.anchor != 3 {
		t.Fatalf("anchor should stay at 3 after range, got %d", s.anchor)
	}
}

// TestSelection_ClickRange_FilterExcludesHiddenLines guards the filter-view
// bug: a range over a filtered buffer must select only the visible (matching)
// lines, never the hidden non-matching indices between them — otherwise `y`
// copies lines the user never saw.
func TestSelection_ClickRange_FilterExcludesHiddenLines(t *testing.T) {
	s := &selectionState{}
	s.clickPlain(2) // anchor on visible line 2
	visible := map[int]bool{2: true, 5: true, 9: true}

	s.clickRange(9, visible)

	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{2, 5, 9}) {
		t.Fatalf("range must select only visible lines, got %v", got)
	}
}

func TestSelection_ClickRangeReversed(t *testing.T) {
	s := &selectionState{}
	s.clickPlain(7)
	s.clickRange(3, nil)
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{3, 4, 5, 6, 7}) {
		t.Fatalf("reversed range wrong: %v", got)
	}
}

func TestSelection_ClickRangeWithoutAnchorCollapses(t *testing.T) {
	s := &selectionState{}
	s.clickRange(4, nil)
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("range without prior selection should be a single line, got %v", got)
	}
	if s.anchor != 4 {
		t.Fatalf("anchor should be set to clicked line, got %d", s.anchor)
	}
}

func TestSelection_ShiftEvictedDropsHead(t *testing.T) {
	s := &selectionState{}
	s.clickPlain(2)
	s.clickToggle(5)
	s.clickToggle(9)
	s.shiftEvicted(3) // drops 2 (becomes -1), shifts 5→2, 9→6
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{2, 6}) {
		t.Fatalf("shift evicted wrong: %v", got)
	}
	// Anchor was 9 → 6, which is still in the set.
	if s.anchor != 6 {
		t.Fatalf("anchor should follow shift, got %d", s.anchor)
	}
}

func TestSelection_ShiftEvictedRehomesLostAnchor(t *testing.T) {
	// Anchor sits on a line that the eviction will sweep away. The
	// remaining lines should survive the shift, and the anchor must
	// rehome to the smallest surviving index so a follow-up shift-
	// click still has a valid origin to extend from.
	s := &selectionState{}
	s.clickToggle(4)
	s.clickToggle(8)
	s.anchor = 1 // anchor outside the selection set, in the eviction range
	s.shiftEvicted(2)
	if got := sortedKeys(s.lines); !reflect.DeepEqual(got, []int{2, 6}) {
		t.Fatalf("survived lines wrong: %v", got)
	}
	if s.anchor != 2 {
		t.Fatalf("anchor should rehome to smallest survivor, got %d", s.anchor)
	}
}

func TestSelection_SnapshotInBufferOrder(t *testing.T) {
	s := &selectionState{}
	s.clickToggle(2)
	s.clickToggle(0)
	s.clickToggle(4)
	buf := []string{"a", "b", "c", "d", "e"}
	got := s.snapshot(buf)
	want := []string{"a", "c", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot order wrong: got %v want %v", got, want)
	}
}

func TestSelection_SnapshotSkipsOutOfRange(t *testing.T) {
	s := &selectionState{}
	s.clickToggle(1)
	s.clickToggle(99)
	buf := []string{"a", "b", "c"}
	got := s.snapshot(buf)
	want := []string{"b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot should skip out-of-range, got %v", got)
	}
}

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"plain":                       "plain",
		"\x1b[31mred\x1b[0m":          "red",
		"a\x1b[1;33mb\x1b[0mc":        "abc",
		"[\x1b[36mtag\x1b[0m] hello":  "[tag] hello",
		"":                            "",
		"trailing\x1b[":               "trailing\x1b[",
	}
	for in, want := range cases {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q): got %q want %q", in, got, want)
		}
	}
}

func TestVisualRowCount_NoWrap(t *testing.T) {
	if got := visualRowCount("hello", 10); got != 1 {
		t.Fatalf("short line wrong: %d", got)
	}
}

func TestVisualRowCount_Wraps(t *testing.T) {
	// 10 visible chars at width 4 → 3 rows: 4 + 4 + 2.
	if got := visualRowCount("abcdefghij", 4); got != 3 {
		t.Fatalf("wrap count wrong: %d", got)
	}
}

func TestVisualRowCount_IgnoresANSI(t *testing.T) {
	// Visible "abcdef" at width 4 → 2 rows, regardless of escape codes.
	in := "ab\x1b[31mcd\x1b[0mef"
	if got := visualRowCount(in, 4); got != 2 {
		t.Fatalf("ANSI ignored wrong: %d", got)
	}
}

func TestVisualRowToLine_MapsBackToOrigin(t *testing.T) {
	// Three lines: "abcdef" wraps to 2 rows, "x" 1 row, "yyyyyyyyyy" wraps to 3 rows.
	// Visual rows 0-1 → line 0, row 2 → line 1, rows 3-5 → line 2.
	perLine := []string{"abcdef", "x", "yyyyyyyyyy"}
	cases := []struct {
		row, want int
	}{
		{0, 0}, {1, 0},
		{2, 1},
		{3, 2}, {4, 2}, {5, 2},
		{6, -1},
	}
	for _, c := range cases {
		if got := visualRowToLine(perLine, 4, c.row); got != c.want {
			t.Errorf("row %d: got %d want %d", c.row, got, c.want)
		}
	}
}

func TestRenderWithSelection_AppliesReverseToWholeLogicalLine(t *testing.T) {
	// Line 1 wraps into 2 chunks at width 4. When selected, both
	// chunks must carry the reverse-video escape so the user sees the
	// entire logical line as selected.
	perLine := []string{"short", "abcdefgh"}
	bufferIx := []int{0, 1}
	sel := &selectionState{}
	sel.clickPlain(1)
	got := renderWithSelection(perLine, bufferIx, sel, 4)
	// Each chunk of line 1 should contain a reverse SGR (ESC[7m).
	if want := "\x1b[7m"; !containsAll(got, want, want) {
		t.Fatalf("expected reverse SGR on both wrapped chunks, got %q", got)
	}
	// Line 0 must NOT be reversed.
	if firstChunk := splitFirstLine(got); contains(firstChunk, "\x1b[7m") {
		t.Fatalf("unselected line picked up reverse style: %q", firstChunk)
	}
}

func TestRenderSelected_RestoresReverseAfterEmbeddedReset(t *testing.T) {
	// A typical hamr line with a coloured rule prefix ends each
	// styled span with a reset. A naive `[7m...[0m` wrapper would
	// turn reverse off at the embedded reset, leaving the tail of
	// the line un-inverted. The implementation must re-emit reverse
	// after every embedded reset so the whole chunk reads as
	// selected.
	got := renderSelected("\x1b[31mrule\x1b[0m hello")
	// Two reverse SGRs expected: the leading one, plus the re-emitted
	// one right after the embedded reset.
	if c := strings.Count(got, "\x1b[7m"); c != 2 {
		t.Fatalf("expected reverse SGR twice (lead + post-reset), got %d in %q", c, got)
	}
	// The embedded reset must be immediately followed by a fresh
	// reverse so subsequent text inverts again.
	if !strings.Contains(got, "\x1b[0m\x1b[7m") {
		t.Fatalf("embedded reset must be followed by a reverse re-emit: %q", got)
	}
}

func TestRenderWithSelection_RespectsBufferIndex(t *testing.T) {
	// Filter mode case: rendered slice has 2 lines but they map to
	// buffer indices 5 and 9. Selecting buffer index 9 should
	// highlight the SECOND rendered line, not "rendered index 9"
	// which doesn't exist.
	perLine := []string{"hit-one", "hit-two"}
	bufferIx := []int{5, 9}
	sel := &selectionState{}
	sel.clickPlain(9)
	got := renderWithSelection(perLine, bufferIx, sel, 80)
	lines := splitLines(got)
	if len(lines) != 2 {
		t.Fatalf("expected 2 rendered lines, got %d (%q)", len(lines), got)
	}
	if contains(lines[0], "\x1b[7m") {
		t.Fatalf("first rendered line should not be selected: %q", lines[0])
	}
	if !contains(lines[1], "\x1b[7m") {
		t.Fatalf("second rendered line (buffer ix 9) should be selected: %q", lines[1])
	}
}

// containsAll reports whether s contains every substring in subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
		s = s[indexOf(s, sub)+len(sub):]
	}
	return true
}

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func splitFirstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

func TestModel_StartDragPlainSelectsLineAtClick(t *testing.T) {
	m := newReadyModelForScroll(8, 8)
	m.height = 10 // status row + 8 viewport rows + hint row
	m.startDrag(tea.MouseMsg{Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})

	sel := m.activeSelection()
	if got := sortedKeys(sel.lines); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("plain press at viewport row 0 should select line 0, got %v", got)
	}
	if !m.drag.active || !m.drag.extend {
		t.Fatalf("press must arm drag (active=%v extend=%v)", m.drag.active, m.drag.extend)
	}
	if m.drag.lastY != 1 {
		t.Fatalf("lastY should track the press Y, got %d", m.drag.lastY)
	}
}

func TestModel_StartDragOutsideViewportIsIgnored(t *testing.T) {
	m := newReadyModelForScroll(8, 8)
	m.height = 10

	// Y=0 is the status bar row; Y=height-1 is the hint bar.
	m.startDrag(tea.MouseMsg{Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.drag.active {
		t.Fatalf("press on status bar row must not arm drag")
	}
	m.startDrag(tea.MouseMsg{Y: 9, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.drag.active {
		t.Fatalf("press on hint bar row must not arm drag")
	}
}

func TestModel_ContinueDragExtendsRangeFromAnchor(t *testing.T) {
	m := newReadyModelForScroll(8, 8)
	m.height = 10
	m.startDrag(tea.MouseMsg{Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.continueDrag(tea.MouseMsg{Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})

	got := sortedKeys(m.activeSelection().lines)
	want := []int{0, 1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drag from row 1 to row 4 should select lines 0-3, got %v", got)
	}
}

func TestModel_CtrlPressDoesNotArmExtend(t *testing.T) {
	m := newReadyModelForScroll(8, 8)
	m.height = 10
	m.startDrag(tea.MouseMsg{Y: 1, Ctrl: true, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})

	if !m.drag.active {
		t.Fatalf("ctrl press still arms drag (release-tracking)")
	}
	if m.drag.extend {
		t.Fatalf("ctrl press must NOT enable extend — ctrl is a single-line toggle")
	}
	// Motion under ctrl should not extend the selection.
	m.continueDrag(tea.MouseMsg{Y: 4, Ctrl: true, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	got := sortedKeys(m.activeSelection().lines)
	if !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("ctrl drag must not extend, got %v", got)
	}
}

func TestModel_EndDragClearsState(t *testing.T) {
	m := newReadyModelForScroll(8, 8)
	m.height = 10
	m.startDrag(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})

	m.endDrag()
	if m.drag.active || m.drag.extend || m.drag.lastY != 0 {
		t.Fatalf("endDrag must zero the state, got %+v", m.drag)
	}
	// Selection itself is independent of the drag state and should
	// survive — release leaves the picked lines selected.
	if !m.activeSelection().hasAny() {
		t.Fatalf("endDrag must not clear the selection itself")
	}
}

func TestModel_LineAtScreenYClampsAboveAndBelow(t *testing.T) {
	// 8 lines, viewport height 4, scrolled to top so visible rows are
	// 0..3 → lines 0..3.
	m := newReadyModelForScroll(4, 8)
	m.height = 6 // status + 4 viewport + hint
	m.view.SetYOffset(0)

	if got := m.lineAtScreenY(0, true); got != 0 {
		t.Errorf("Y above viewport should clamp to top visible line (0), got %d", got)
	}
	if got := m.lineAtScreenY(5, true); got != 3 {
		t.Errorf("Y below viewport should clamp to bottom visible line (3), got %d", got)
	}
	if got := m.lineAtScreenY(2, true); got != 1 {
		t.Errorf("Y inside viewport should map to its row, got %d", got)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// TestModel_LineAtScreenY_VoidClickIgnoredNotClamped guards the click-empty-rows
// bug: a plain click on the empty void below a short buffer must be ignored
// (-1), not clamped to the last line — the clamp is only for a drag past the end.
func TestModel_LineAtScreenY_VoidClickIgnoredNotClamped(t *testing.T) {
	// 2 lines in a height-4 viewport → rows 2,3 are empty void below the buffer.
	m := newReadyModelForScroll(4, 2)
	m.height = 6 // status + 4 viewport + hint
	m.view.SetYOffset(0)

	// Screen Y=4 → viewport row 3, the empty void below the 2 lines.
	if got := m.lineAtScreenY(4, false); got != -1 {
		t.Fatalf("initial click on empty void must return -1 (ignore), got %d", got)
	}
	// A drag past the end still clamps to the last line.
	if got := m.lineAtScreenY(4, true); got != 1 {
		t.Fatalf("drag past end should clamp to last line (1), got %d", got)
	}
}
