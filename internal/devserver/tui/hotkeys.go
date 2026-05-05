package tui

import "github.com/FyrmForge/hamr/internal/devserver"

// HotkeySource is the bubbletea-side implementation of
// devserver.HotkeySource. The model dispatches r/o/q keys to Send; the
// runner's hotkey loop consumes them via Actions().
//
// c (clear) and m (run) are handled inside the model and never enter
// this channel — c clears the viewport, m opens the Makefile-target
// fuzzy palette.
type HotkeySource struct {
	ch chan devserver.HotkeyAction
}

// NewHotkeySource returns a buffered source. The buffer absorbs short
// bursts (e.g. mash 'r' a few times) without blocking the bubbletea Update
// loop.
func NewHotkeySource() *HotkeySource {
	return &HotkeySource{ch: make(chan devserver.HotkeyAction, 8)}
}

// Actions implements devserver.HotkeySource.
func (h *HotkeySource) Actions() <-chan devserver.HotkeyAction { return h.ch }

// Send pushes an action non-blockingly. If the buffer is full the action is
// dropped — the runner is already busy and another keypress is the user's
// problem to repeat.
func (h *HotkeySource) Send(a devserver.HotkeyAction) {
	select {
	case h.ch <- a:
	default:
	}
}

var _ devserver.HotkeySource = (*HotkeySource)(nil)
