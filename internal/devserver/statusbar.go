package devserver

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// StatusBar renders a persistent hotkey bar at the bottom of the terminal
// using ANSI scroll regions. All normal output is confined above the bar.
type StatusBar struct {
	mu         sync.Mutex
	fd         int
	running    bool
	sigCh      chan os.Signal
	stopCh     chan struct{}
	errorState *ErrorState
}

// bar content constants.
const (
	barDim   = "\033[2m"
	barBold  = "\033[1m"
	barReset = "\033[0m"
	barRed   = "\033[1;31m"
	barGreen = "\033[32m"
)

// barKeys is the hotkey hints portion (without the leading emoji).
var barKeys = barDim +
	"  " + barBold + "r" + barReset + barDim + " rebuild" +
	"  " +
	barBold + "o" + barReset + barDim + " open" +
	"  " +
	barBold + "c" + barReset + barDim + " clear" +
	"  " +
	barBold + "q" + barReset + barDim + " quit" +
	barReset

const barBaseVisibleLen = 39 // "  🔨" (4 cols) + barKeys (35 visible chars)

// SetErrorState wires the error state so the bar redraws on error changes.
func (sb *StatusBar) SetErrorState(es *ErrorState) {
	sb.mu.Lock()
	sb.errorState = es
	sb.mu.Unlock()
	es.OnChange(sb.Redraw)
}

// buildBarContent returns the bar string with a colored emoji and error indicator.
func (sb *StatusBar) buildBarContent(width int) string {
	sb.mu.Lock()
	es := sb.errorState
	sb.mu.Unlock()

	hasErrors := es != nil && es.HasErrors()

	// Color the emoji: green when healthy, red when errors.
	var hamr string
	if hasErrors {
		hamr = "  " + barRed + "🔨" + barReset
	} else {
		hamr = "  " + barGreen + "🔨" + barReset
	}
	base := hamr + barKeys

	if !hasErrors {
		return base
	}

	names := es.RuleNames()

	// "  ERR rule1, rule2" — red bold ERR, dim rule names.
	errPrefix := "  " + barRed + "ERR" + barReset + barDim + " "
	errPrefixVisibleLen := 6 // "  ERR "

	available := width - barBaseVisibleLen - errPrefixVisibleLen
	if available < 3 {
		// Not enough room for any names, just show ERR marker.
		return base + "  " + barRed + "ERR" + barReset
	}

	joined := strings.Join(names, ", ")
	if len(joined) > available {
		if available <= 3 {
			joined = joined[:available]
		} else {
			joined = joined[:available-1] + "…"
		}
	}

	return base + errPrefix + joined + barReset
}

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
	buf.WriteString("\033[r")                 // reset scroll region
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", h) // clear bar row
	fmt.Fprintf(&buf, "\033[%d;1H", h-1)      // cursor above bar

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
	w, h, err := term.GetSize(sb.fd)
	if err != nil || h < 3 {
		return
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\033[1;%dr", h-1)      // set scroll region (rows 1..h-1)
	fmt.Fprintf(&buf, "\033[%d;1H\033[2K", h) // move to bar row, clear it
	buf.WriteString(sb.buildBarContent(w))    // bar text
	fmt.Fprintf(&buf, "\033[%d;1H", h-1)      // cursor to bottom of scroll region

	termMu.Lock()
	_, _ = os.Stdout.Write(buf.Bytes())
	termMu.Unlock()
}
