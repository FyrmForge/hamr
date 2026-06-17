package tui

import (
	"strings"
	"testing"
)

func TestSearch_StartsClosed(t *testing.T) {
	var s searchState
	if s.active() {
		t.Fatal("zero value must be closed")
	}
	if s.prompting() {
		t.Fatal("zero value must not be prompting")
	}
}

func TestSearch_OpenEntersPromptStage(t *testing.T) {
	var s searchState
	s.open()
	if !s.prompting() {
		t.Fatal("open() should put state in prompting")
	}
	if s.query != "" {
		t.Fatalf("opened query should be empty, got %q", s.query)
	}
}

func TestSearch_AppendAndBackspace(t *testing.T) {
	var s searchState
	s.open()
	s.appendRune('e')
	s.appendRune('r')
	s.appendRune('r')
	if s.query != "err" {
		t.Fatalf("query: got %q", s.query)
	}
	s.backspace()
	if s.query != "er" {
		t.Fatalf("after backspace: got %q", s.query)
	}
	s.backspace()
	s.backspace()
	s.backspace() // extra; should be no-op
	if s.query != "" {
		t.Fatalf("expected empty after over-backspace, got %q", s.query)
	}
}

func TestSearch_AppendOnlyValidInPromptStage(t *testing.T) {
	var s searchState
	// not open — append should be a no-op
	s.appendRune('x')
	if s.query != "" {
		t.Fatalf("appendRune outside prompt must not record, got %q", s.query)
	}
}

func TestSearch_CommitWithMatchesActivates(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "error" {
		s.appendRune(r)
	}
	buf := []string{
		"all good",
		"got an error here",
		"another ERROR line",
		"nothing",
	}
	s.commit(buf)

	if s.stage != searchActive {
		t.Fatalf("commit with hits should activate, got stage=%d", s.stage)
	}
	if len(s.matches) != 2 {
		t.Fatalf("expected 2 matches (case-insensitive), got %d", len(s.matches))
	}
	if s.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", s.cursor)
	}
	if got := s.matches[0].line; got != 1 {
		t.Fatalf("first match line: want 1, got %d", got)
	}
}

func TestSearch_CommitEmptyQueryClosesSearch(t *testing.T) {
	var s searchState
	s.open()
	s.commit([]string{"line"})
	if s.active() {
		t.Fatal("commit on empty query should close the search")
	}
}

func TestSearch_CommitWithNoMatchesStillActivates(t *testing.T) {
	// Lets the hint bar show "[no matches]"; pressing esc closes.
	var s searchState
	s.open()
	for _, r := range "needle" {
		s.appendRune(r)
	}
	s.commit([]string{"hay", "stack"})
	if s.stage != searchActive {
		t.Fatalf("zero-match commit should activate (so the bar can show feedback), got stage=%d", s.stage)
	}
	if len(s.matches) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(s.matches))
	}
}

func TestSearch_NextWrapsAndPrevWrapsBackward(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.commit([]string{"foo", "foo bar", "foo baz"})
	if len(s.matches) != 3 {
		t.Fatalf("setup: want 3 matches, got %d", len(s.matches))
	}

	s.next() // 0 -> 1
	s.next() // 1 -> 2
	s.next() // 2 -> 0 (wrap)
	if s.cursor != 0 {
		t.Fatalf("forward wraparound: want cursor=0, got %d", s.cursor)
	}
	s.prev() // 0 -> 2 (wrap)
	if s.cursor != 2 {
		t.Fatalf("backward wraparound: want cursor=2, got %d", s.cursor)
	}
}

func TestSearch_RecomputeKeepsCursorOnSamePriorMatch(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.commit([]string{"foo a", "foo b"})
	s.next() // cursor on second match (line 1)

	// Append a fresh line; recompute should keep the cursor anchored to
	// the original "line 1" hit, not jump to the new one.
	s.recompute([]string{"foo a", "foo b", "foo c"}, 0)
	if len(s.matches) != 3 {
		t.Fatalf("expected 3 matches after grow, got %d", len(s.matches))
	}
	if s.matches[s.cursor].line != 1 {
		t.Fatalf("cursor should remain on prior match (line 1), got line=%d cursor=%d", s.matches[s.cursor].line, s.cursor)
	}
}

func TestSearch_RecomputeResetsCursorWhenMatchesGone(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.commit([]string{"foo a", "foo b"})
	s.next()

	// Buffer rotation has dropped the matched lines (cap exceeded).
	s.recompute([]string{"unrelated", "still no match"}, 0)
	if len(s.matches) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(s.matches))
	}
	if s.cursor != 0 {
		t.Fatalf("cursor should reset to 0 when matches vanish, got %d", s.cursor)
	}
}

func TestSearch_RecomputeNoOpWhenClosed(t *testing.T) {
	var s searchState
	// stage closed: should ignore
	s.recompute([]string{"foo"}, 0)
	if s.active() {
		t.Fatal("recompute on closed state should not activate the search")
	}
}

func TestSearch_RecomputeDuringPromptingUpdatesMatches(t *testing.T) {
	// Live search: every keystroke calls recompute against the current
	// buffer while still in prompting stage. Cursor must always sit at
	// 0 (incremental search jumps to first hit).
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.recompute([]string{"foo a", "bar", "foo c"}, 0)

	if s.stage != searchPrompting {
		t.Fatalf("recompute must not change stage, got %d", s.stage)
	}
	if len(s.matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(s.matches))
	}
	if s.cursor != 0 {
		t.Fatalf("during prompting cursor must reset to 0, got %d", s.cursor)
	}
}

func TestSearch_RecomputeDuringPromptingResetsCursorEvenWithPrior(t *testing.T) {
	// Even if the prior cursor pointed somewhere, recompute during
	// prompting should snap back to 0 — incremental search semantics.
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.recompute([]string{"foo", "foo"}, 0)
	s.cursor = 1 // simulate pre-existing position

	s.recompute([]string{"foo", "foo", "foo"}, 0)
	if s.cursor != 0 {
		t.Fatalf("cursor must reset on every prompt-stage recompute, got %d", s.cursor)
	}
}

// TestSearch_RecomputeFollowsCursorAcrossEviction guards the maxLogs bug:
// when the bounded buffer trims its head, every match line index shifts down.
// recompute must realign the prior anchor by the evicted count so the active
// cursor stays on the same logical match instead of snapping back to hit 0.
func TestSearch_RecomputeFollowsCursorAcrossEviction(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.commit([]string{"foo0", "foo1", "foo2"}) // active, 3 matches, cursor 0

	s.next()
	s.next()
	if got := s.currentMatch().line; got != 2 {
		t.Fatalf("setup: expected cursor on line 2, got %d", got)
	}

	// One head line evicted: "foo0" gone, the rest shift down by 1, "foo3" added.
	s.recompute([]string{"foo1", "foo2", "foo3"}, 1)

	// The match the user was navigating ("foo2") is now at line 1 — the cursor
	// must follow it, not reset to 0.
	if got := s.currentMatch().line; got != 1 {
		t.Fatalf("cursor should follow the matched line across eviction, got line %d", got)
	}
}

func TestSearch_CommitWithoutBufferKeepsLiveMatches(t *testing.T) {
	// With live search, matches are computed during prompting. Commit
	// (Enter) just flips the stage; it shouldn't drop the matches even
	// if no buffer is supplied.
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.recompute([]string{"foo a", "foo b"}, 0) // simulates per-keystroke recompute
	if len(s.matches) != 2 {
		t.Fatalf("setup: expected 2 matches, got %d", len(s.matches))
	}

	s.commit(nil)

	if s.stage != searchActive {
		t.Fatalf("commit should activate, got stage=%d", s.stage)
	}
	if len(s.matches) != 2 {
		t.Fatalf("commit must preserve live matches, got %d", len(s.matches))
	}
}

func TestSearch_CurrentMatchAvailableDuringPrompting(t *testing.T) {
	// The model relies on currentMatch to scroll the viewport while the
	// user is still typing. Returning zero value would break that.
	var s searchState
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.recompute([]string{"foo here", "and foo there"}, 0)

	cur := s.currentMatch()
	if cur.line != 0 {
		t.Fatalf("currentMatch during prompting should point at the first hit, got line=%d", cur.line)
	}
}

func TestSearch_CancelReturnsToClosed(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "x" {
		s.appendRune(r)
	}
	s.commit([]string{"xy"})
	s.cancel()
	if s.active() {
		t.Fatal("cancel must close the search")
	}
	if s.query != "" {
		t.Fatalf("cancel should clear the query, got %q", s.query)
	}
	if len(s.matches) != 0 {
		t.Fatalf("cancel should drop matches, got %d", len(s.matches))
	}
}

func TestFindMatches_CaseInsensitiveAndMultiHitPerLine(t *testing.T) {
	got := findMatches([]string{
		"Error then ERROR then error",
		"none here",
	}, "error")
	if len(got) != 3 {
		t.Fatalf("want 3 hits (3 occurrences on line 0), got %d: %+v", len(got), got)
	}
	for i, m := range got {
		if m.line != 0 {
			t.Fatalf("hit %d should be on line 0, got %d", i, m.line)
		}
	}
	// Verify offsets are in increasing order — render relies on this.
	if got[0].start >= got[1].start || got[1].start >= got[2].start {
		t.Fatalf("matches must be in scan order, got %+v", got)
	}
}

func TestFindMatches_EmptyNeedleReturnsNothing(t *testing.T) {
	if got := findMatches([]string{"line"}, ""); len(got) != 0 {
		t.Fatalf("empty needle must produce zero matches, got %d", len(got))
	}
}

func TestSearch_FilterDefaultsOff(t *testing.T) {
	var s searchState
	s.open()
	if s.filtering {
		t.Fatal("filter should default to off after open()")
	}
}

func TestSearch_ToggleFilterFlips(t *testing.T) {
	var s searchState
	s.toggleFilter()
	if !s.filtering {
		t.Fatal("first toggle should turn filter on")
	}
	s.toggleFilter()
	if s.filtering {
		t.Fatal("second toggle should turn filter off")
	}
}

func TestSearch_OpenResetsFilter(t *testing.T) {
	var s searchState
	s.toggleFilter()
	s.open()
	if s.filtering {
		t.Fatal("open() must reset filter so each new search starts unfiltered")
	}
}

func TestSearch_CancelResetsFilter(t *testing.T) {
	var s searchState
	s.toggleFilter()
	s.cancel()
	if s.filtering {
		t.Fatal("cancel must reset filter along with the rest of the state")
	}
}

func TestSearch_MatchedLineOrderIsUniqueAndSorted(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "x" {
		s.appendRune(r)
	}
	// "x" appears on lines 0, 1 (twice), and 3 — matchedLineOrder must
	// dedupe line 1 and preserve top-down order.
	s.recompute([]string{"x", "xx", "y", "x"}, 0)

	got := s.matchedLineOrder()
	want := []int{0, 1, 3}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: want %d, got %d", i, want[i], got[i])
		}
	}
}

func TestSearch_FilteredCursorLineMapsToFilteredIndex(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "x" {
		s.appendRune(r)
	}
	s.recompute([]string{"x", "no", "x", "no", "x"}, 0)
	s.commit(nil)
	s.toggleFilter()

	// matchedLineOrder is [0, 2, 4]; cursor 0 → row 0, cursor 1 → row 1, cursor 2 → row 2.
	for i, want := range []int{0, 1, 2} {
		s.cursor = i
		got := s.filteredCursorLine()
		if got != want {
			t.Fatalf("cursor=%d: want filtered row %d, got %d", i, want, got)
		}
	}
}

func TestSearch_FilteredCursorLineZeroWhenFilterOff(t *testing.T) {
	var s searchState
	s.open()
	for _, r := range "x" {
		s.appendRune(r)
	}
	s.recompute([]string{"x"}, 0)
	s.commit(nil)
	// filtering is off → filteredCursorLine returns 0 (caller should
	// use currentMatch().line instead).
	if got := s.filteredCursorLine(); got != 0 {
		t.Fatalf("filteredCursorLine off: want 0, got %d", got)
	}
}

func TestRenderedLines_FilterOnlyEmitsMatchedLines(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.hamrLogs = []string{
		"all good",
		"got an error here",
		"ok",
		"another error",
	}
	s := m.activeSearch()
	s.open()
	for _, r := range "err" {
		s.appendRune(r)
	}
	s.recompute(m.hamrLogs, 0)
	s.commit(nil)
	s.toggleFilter()

	lines, ix := m.renderedLines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 emitted lines, got %d (%v)", len(lines), lines)
	}
	if !strings.Contains(lines[0], "got an error here") || !strings.Contains(lines[1], "another error") {
		t.Fatalf("filtered output content wrong: %v", lines)
	}
	// Buffer indices should map back to the matched lines' positions.
	if ix[0] != 1 || ix[1] != 3 {
		t.Fatalf("buffer indices wrong: got %v want [1 3]", ix)
	}
}

func TestRenderedLines_FilterIgnoredDuringPrompting(t *testing.T) {
	// Even with filtering=true, prompting should still render every
	// line (with inline highlights) so the user has buffer context
	// while typing.
	m := NewModel(NewHotkeySource())
	m.hamrLogs = []string{"foo", "bar", "foo"}
	s := m.activeSearch()
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.recompute(m.hamrLogs, 0)
	s.filtering = true // carryover simulation; open() normally resets it

	lines, _ := m.renderedLines()
	if len(lines) != 3 {
		t.Fatalf("expected all 3 lines during prompting, got %d (%v)", len(lines), lines)
	}
	if !strings.Contains(lines[1], "bar") {
		t.Fatalf("non-matching line must stay visible while prompting: %q", lines[1])
	}
}

// TestFindMatches_UnicodeCaseChangesByteLength guards the highlight-offset bug:
// İ (U+0130) lower-cases to a single 'i' (2 bytes → 1), shifting later byte
// offsets in the lower-cased copy. Match offsets must map back to the original
// line so highlight rendering slices the right bytes rather than garbling them.
func TestFindMatches_UnicodeCaseChangesByteLength(t *testing.T) {
	line := "İ foo" // İ before the match changes byte length under ToLower
	matches := findMatches([]string{line}, "foo")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]
	if got := line[m.start:m.end]; got != "foo" {
		t.Fatalf("offsets must slice the original to %q, got %q (start=%d end=%d)", "foo", got, m.start, m.end)
	}
}
