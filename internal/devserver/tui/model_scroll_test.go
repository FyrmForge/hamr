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
