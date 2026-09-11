package devserver

import (
	"os/exec"
	"runtime"
)

// HotkeyAction represents a user-triggered hotkey action.
type HotkeyAction int

const (
	HotkeyRebuild HotkeyAction = iota
	HotkeyOpenBrowser
	HotkeyQuit
	HotkeyMCPToggle
	// HotkeyRestart tears the dev server down and re-runs its whole startup
	// lifecycle (docker compose, port resolution, .env injection, builds,
	// daemons, watcher) in-place, leaving the TUI running. The escape hatch
	// for state the runner only reads at startup: a clashing port, an edited
	// .env, a container that came up wrong.
	HotkeyRestart
)

// HotkeySource emits hotkey actions for the dev runner to consume. The TUI
// implements this with a bubbletea-backed adapter; the runner reads from
// Actions() in its event loop. A nil channel means no source is attached
// and the loop should never fire on it.
type HotkeySource interface {
	Actions() <-chan HotkeyAction
}

// noopHotkeySource is the default Runner.hotkeys when nothing is wired
// (most tests). Its channel is nil, so a select over it blocks forever
// and never fires.
type noopHotkeySource struct{}

func (noopHotkeySource) Actions() <-chan HotkeyAction { return nil }

// openBrowser opens the given URL in the default browser. It's a var so tests
// can substitute a no-op/recorder.
var openBrowser = func(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux, freebsd, etc.
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
