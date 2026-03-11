package devserver

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// StatusBar renders a persistent hotkey bar at the bottom of the terminal
// using ANSI scroll regions. All normal output is confined above the bar.
type StatusBar struct {
	mu      sync.Mutex
	fd      int
	running bool
	sigCh   chan os.Signal
	stopCh  chan struct{}
}

// bar content constants.
const (
	barDim   = "\033[2m"
	barBold  = "\033[1m"
	barReset = "\033[0m"
)

// barContent is the pre-built bar string (no ANSI cursor/region commands).
var barContent = barDim + "  " +
	barBold + "r" + barReset + barDim + " rebuild" +
	"  " +
	barBold + "o" + barReset + barDim + " open" +
	"  " +
	barBold + "c" + barReset + barDim + " clear" +
	"  " +
	barBold + "q" + barReset + barDim + " quit" +
	barReset

// Start activates the status bar. It sets a scroll region that reserves the
// bottom row for the hotkey hints and listens for SIGWINCH to handle resizes.
// In non-TTY environments this is a silent no-op.
func (sb *StatusBar) Start() {
	sb.fd = int(os.Stdout.Fd())
	if !term.IsTerminal(sb.fd) {
		return
	}

	sb.mu.Lock()
	sb.running = true
	sb.mu.Unlock()

	sb.draw()

	// Re-layout on terminal resize.
	sb.sigCh = make(chan os.Signal, 1)
	sb.stopCh = make(chan struct{})
	signal.Notify(sb.sigCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-sb.sigCh:
				sb.Redraw()
			case <-sb.stopCh:
				return
			}
		}
	}()
}

// Stop restores the full scroll region and cleans up.
func (sb *StatusBar) Stop() {
	sb.mu.Lock()
	wasRunning := sb.running
	sb.running = false
	sb.mu.Unlock()

	if !wasRunning {
		return
	}

	if sb.sigCh != nil {
		signal.Stop(sb.sigCh)
		close(sb.stopCh)
	}

	_, h, err := term.GetSize(sb.fd)
	if err != nil {
		termMu.Lock()
		_, _ = os.Stdout.Write([]byte("\033[r"))
		termMu.Unlock()
		return
	}

	var buf bytes.Buffer
	buf.WriteString("\033[r")                            // reset scroll region
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", h)           // clear bar row
	fmt.Fprintf(&buf, "\033[%d;1H", h-1)                // cursor above bar

	termMu.Lock()
	_, _ = os.Stdout.Write(buf.Bytes())
	termMu.Unlock()
}

// Redraw recalculates the layout and redraws the bar. Safe to call from
// any goroutine (e.g. SIGWINCH handler, hotkey handler).
func (sb *StatusBar) Redraw() {
	sb.mu.Lock()
	if !sb.running {
		sb.mu.Unlock()
		return
	}
	sb.mu.Unlock()

	sb.draw()
}

// draw builds the entire escape sequence in a buffer and writes it in a
// single termMu-protected call to prevent interleaving with child output.
func (sb *StatusBar) draw() {
	_, h, err := term.GetSize(sb.fd)
	if err != nil || h < 3 {
		return
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\033[1;%dr", h-1)                 // set scroll region (rows 1..h-1)
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", h)            // move to bar row, clear it
	buf.WriteString(barContent)                           // bar text
	fmt.Fprintf(&buf, "\033[%d;1H", h-1)                 // cursor to bottom of scroll region

	termMu.Lock()
	_, _ = os.Stdout.Write(buf.Bytes())
	termMu.Unlock()
}
