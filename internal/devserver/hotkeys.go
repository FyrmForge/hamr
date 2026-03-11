package devserver

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/term"
)

// HotkeyAction represents a user-triggered hotkey action.
type HotkeyAction int

const (
	HotkeyRebuild       HotkeyAction = iota
	HotkeyOpenBrowser
	HotkeyClearTerminal
	HotkeyQuit
)

// HotkeyReader reads single key presses from stdin in raw terminal mode.
type HotkeyReader struct {
	ch     chan HotkeyAction
	cancel context.CancelFunc
}

// Start begins reading hotkeys. It puts the terminal into raw mode and reads
// single bytes in a goroutine. The caller should select on Actions().
// In non-TTY environments (CI, pipes) this is a no-op.
func (h *HotkeyReader) Start(ctx context.Context) {
	h.ch = make(chan HotkeyAction, 1)

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return
	}

	ctx, h.cancel = context.WithCancel(ctx)

	// Restore terminal on context cancellation.
	go func() {
		<-ctx.Done()
		_ = term.Restore(fd, oldState)
	}()

	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			var action HotkeyAction
			switch buf[0] {
			case 'r':
				action = HotkeyRebuild
			case 'o':
				action = HotkeyOpenBrowser
			case 'c':
				action = HotkeyClearTerminal
			case 'q', 0x03: // q or Ctrl+C
				action = HotkeyQuit
			default:
				continue
			}
			select {
			case h.ch <- action:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Actions returns the channel that receives hotkey actions.
func (h *HotkeyReader) Actions() <-chan HotkeyAction {
	if h.ch == nil {
		// Return a nil channel that blocks forever.
		return nil
	}
	return h.ch
}

// Stop cancels the hotkey reader and restores the terminal.
func (h *HotkeyReader) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
}

// openBrowser opens the given URL in the default browser.
func openBrowser(url string) {
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

// clearTerminal clears the terminal screen using ANSI escape codes.
func clearTerminal() {
	termMu.Lock()
	_, _ = os.Stdout.Write([]byte("\033[2J\033[H"))
	_, _ = os.Stderr.Write([]byte("\033[2J\033[H"))
	termMu.Unlock()
}
