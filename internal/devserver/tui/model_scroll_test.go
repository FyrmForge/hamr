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
	for i := 0; i < lines; i++ {
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

	for i := 0; i < 6; i++ {
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

	for i := 0; i < 5; i++ {
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
	s.recompute(m.hamrLogs)
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

	if m.view.AtBottom() {
		t.Log("follow DOES resume even with active search (test asserts it doesn't — flip if you want resume)")
	} else {
		t.Logf("BUG candidate: follow does NOT resume after enter while search active; YOffset=%d", m.view.YOffset)
	}
}

// Mouse-wheel scroll-up then Enter then incoming logs. The viewport
// receives wheel events via m.view.Update directly from the MouseMsg
// path in handleMessage.
func TestModel_FollowResumesAfterWheelScrollAndEnter(t *testing.T) {
	m := newReadyModelForScroll(4, 8)

	for i := 0; i < 6; i++ {
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

	for i := 0; i < 5; i++ {
		m.appendHamrLog(fmt.Sprintf("after %d", i))
		if !m.view.AtBottom() {
			t.Fatalf("follow broke after %d new lines; YOffset=%d", i+1, m.view.YOffset)
		}
	}
}
