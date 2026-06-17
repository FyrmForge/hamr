package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func newReadyModelForScroll(height int, lines int) *Model {
	m := NewModel(NewHotkeySource())
	m.ready = true
	m.view = viewport.New(80, height)
	m.view.KeyMap = scrollKeyMap()
	for i := range lines {
		m.hamrLogs = append(m.hamrLogs, fmt.Sprintf("line %d", i))
	}
	m.view.SetContent("")
	m.refreshViewport()
	m.view.GotoBottom()
	return m
}

func TestModel_RefreshViewportKeepsTailWhenAtBottom(t *testing.T) {
	m := newReadyModelForScroll(4, 8)
	if !m.view.AtBottom() {
		t.Fatal("setup: expected viewport at bottom")
	}

	m.appendHamrLog("line 8")

	if !m.view.AtBottom() {
		t.Fatal("new logs should keep following when already at bottom")
	}
}

func TestModel_RefreshViewportDoesNotYankWhenScrolledUp(t *testing.T) {
	m := newReadyModelForScroll(4, 8)
	m.view.SetYOffset(0)
	if m.view.AtBottom() {
		t.Fatal("setup: expected viewport scrolled away from bottom")
	}

	m.appendHamrLog("line 8")

	if m.view.AtBottom() {
		t.Fatal("new logs should not yank viewport to bottom when user scrolled up")
	}
	if got := m.view.YOffset; got != 0 {
		t.Fatalf("expected y offset to stay pinned at top, got %d", got)
	}
}

func TestModel_EnterJumpsBackToBottom(t *testing.T) {
	m := newReadyModelForScroll(4, 8)
	m.view.SetYOffset(0)
	if m.view.AtBottom() {
		t.Fatal("setup: expected viewport scrolled away from bottom")
	}

	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = got.(*Model)

	if !m.view.AtBottom() {
		t.Fatal("enter should jump viewport back to bottom")
	}
}

// Reproduces the user-reported bug: scroll up, press Enter to jump back
// to the bottom, then watch whether subsequent log lines keep the view
// stuck to the tail. If Enter re-engages follow correctly, the viewport
// must remain at the bottom after appendHamrLog.
func TestModel_FollowResumesAfterScrollUpAndEnter(t *testing.T) {
	m := newReadyModelForScroll(4, 8)
	m.view.SetYOffset(0)

	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = got.(*Model)
	if !m.view.AtBottom() {
		t.Fatal("setup: enter should put viewport at bottom")
	}

	m.appendHamrLog("line 8")

	if !m.view.AtBottom() {
		t.Fatalf("expected follow to resume after enter; YOffset=%d", m.view.YOffset)
	}
}

// Scrolling up with the arrow keys (which route through viewport.Update
// via scrollKeyMap) then Enter must still re-engage follow. The previous
// test used SetYOffset directly, which skips the keymap path.
func TestModel_FollowResumesAfterArrowScrollAndEnter(t *testing.T) {
	m := newReadyModelForScroll(4, 8)

	for range 6 {
		got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
		m = got.(*Model)
	}
	if m.view.AtBottom() {
		t.Fatal("setup: expected arrow-up to leave viewport off the bottom")
	}

	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = got.(*Model)
	if !m.view.AtBottom() {
		t.Fatalf("enter should land at bottom; YOffset=%d", m.view.YOffset)
	}

	for i := range 5 {
		m.appendHamrLog(fmt.Sprintf("after %d", i))
		if !m.view.AtBottom() {
			t.Fatalf("follow broke after %d new lines; YOffset=%d", i+1, m.view.YOffset)
		}
	}
}

// If a search is committed (s.stage == searchActive), refreshViewport
// suspends auto-follow on incoming logs by design. Enter currently only
// calls GotoBottom — so the help text "jump back to bottom and resume
// tailing" is misleading when a search is up: the jump happens, but the
// next log line breaks the tail again.
func TestModel_FollowDoesNotResumeAfterEnterWhileSearchActive(t *testing.T) {
	m := newReadyModelForScroll(4, 8)
	s := m.activeSearch()
	s.open()
	s.appendRune('l')
	s.appendRune('i')
	s.recompute(m.hamrLogs, 0)
	s.commit(m.hamrLogs)
	if s.stage != searchActive {
		t.Fatalf("setup: expected searchActive stage, got %v", s.stage)
	}
	m.refreshViewport()

	m.view.SetYOffset(0)
	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = got.(*Model)
	if !m.view.AtBottom() {
		t.Fatalf("enter should jump to bottom; YOffset=%d", m.view.YOffset)
	}

	m.appendHamrLog("line 8")

	// With an active search, a new log must NOT yank the viewport back to the
	// bottom — the user is reading search results.
	if m.view.AtBottom() {
		t.Fatalf("follow must not resume after Enter while search is active; YOffset=%d", m.view.YOffset)
	}
}

// Mouse-wheel scroll-up then Enter then incoming logs. The viewport
// receives wheel events via m.view.Update directly from the MouseMsg
// path in handleMessage.
func TestModel_FollowResumesAfterWheelScrollAndEnter(t *testing.T) {
	m := newReadyModelForScroll(4, 8)

	for range 6 {
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
		_ = cmd
	}
	if m.view.AtBottom() {
		t.Fatal("setup: expected wheel-up to leave viewport off the bottom")
	}

	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = got.(*Model)
	if !m.view.AtBottom() {
		t.Fatalf("enter should land at bottom; YOffset=%d", m.view.YOffset)
	}

	for i := range 5 {
		m.appendHamrLog(fmt.Sprintf("after %d", i))
		if !m.view.AtBottom() {
			t.Fatalf("follow broke after %d new lines; YOffset=%d", i+1, m.view.YOffset)
		}
	}
}

func TestModel_AutoScrollSingleChain(t *testing.T) {
	m := newReadyModelForScroll(4, 100)
	m.height = 6         // drag edge is at height-1
	m.view.SetYOffset(0) // at top, so we can scroll down
	// Drag held below the bottom edge (lastY >= height-1 => bottom edge).
	m.drag = dragState{active: true, extend: true, lastY: 20}

	// First motion past the edge starts exactly one chain.
	if cmd := m.startAutoScroll(); cmd == nil {
		t.Fatal("expected an auto-scroll tick to start")
	}
	if !m.drag.ticking {
		t.Fatal("ticking flag should be set after starting a chain")
	}

	// Further motion events past the edge must NOT start additional chains —
	// otherwise N events would run N self-perpetuating chains and multiply the
	// scroll speed.
	for range 5 {
		if cmd := m.startAutoScroll(); cmd != nil {
			t.Fatal("a second concurrent auto-scroll chain was started")
		}
	}

	// When the chain reaches the bottom it stops and clears the flag, so a later
	// drag can start a fresh chain.
	m.view.GotoBottom()
	if cmd := m.continueAutoScroll(); cmd != nil {
		t.Fatal("chain should stop at the bottom")
	}
	if m.drag.ticking {
		t.Fatal("ticking flag should be cleared when the chain ends")
	}
}

// TestModel_ClearActiveLog_RecomputesSearch guards the clear-with-search bug:
// clearing the buffer must recompute the active search so the [k/n] counter and
// n/N targets don't linger against lines that no longer exist.
func TestModel_ClearActiveLog_RecomputesSearch(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.hamrLogs = []string{"foo a", "foo b", "foo c"}

	s := m.activeSearch()
	s.open()
	for _, r := range "foo" {
		s.appendRune(r)
	}
	s.commit(m.hamrLogs)
	if len(s.matches) != 3 {
		t.Fatalf("setup: expected 3 matches, got %d", len(s.matches))
	}

	m.clearActiveLog()

	if len(s.matches) != 0 {
		t.Fatalf("clearing logs must recompute the search to 0 matches, got %d", len(s.matches))
	}
}

// TestModel_SearchScroll_AccountsForWrappedRows guards the visual-row bug:
// SetYOffset takes a visual row, but matches are buffer-line indices. With long
// wrapped lines above the match, scrolling by buffer index leaves the match
// off-screen; the fix converts to the match's visual row first.
func TestModel_SearchScroll_AccountsForWrappedRows(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.ready = true
	m.width = 10
	m.height = 6
	m.view = viewport.New(10, 4) // width 10 forces wrapping

	long := "xxxxxxxxxxxxxxxxxxxxxxxxxxxx" // 28 chars → 3 visual rows at width 10
	for range 5 {
		m.hamrLogs = append(m.hamrLogs, long)
	}
	m.hamrLogs = append(m.hamrLogs, "needle here") // match on buffer line 5
	m.refreshViewport()

	s := m.activeSearch()
	s.open()
	for _, r := range "needle" {
		s.appendRune(r)
	}
	s.commit(m.hamrLogs)
	m.onSearchCursorChange()

	vr := m.matchVisualRow()
	if vr <= 5 {
		t.Fatalf("expected wrapped match visual row > buffer index 5, got %d", vr)
	}
	top, bottom := m.view.YOffset, m.view.YOffset+m.view.Height
	if vr < top || vr >= bottom {
		t.Fatalf("match visual row %d must be visible in viewport [%d,%d)", vr, top, bottom)
	}
}
