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

// VersionStatus indicates the CLI-vs-project version state.
type VersionStatus int

const (
	VersionOK       VersionStatus = iota // versions match or no project version
	VersionDev                           // CLI is a dev build
	VersionMismatch                      // CLI major.minor differs from project
	VersionUpdate                        // newer version available on GitHub
)

// StatusBar renders a persistent hotkey bar at the bottom of the terminal
// using ANSI scroll regions. All normal output is confined above the bar.
type StatusBar struct {
	mu            sync.Mutex
	fd            int
	running       bool
	sigCh         chan os.Signal
	stopCh        chan struct{}
	errorState    *ErrorState
	versionStatus VersionStatus
	versionMsg    string // e.g. "cli=0.4.0 project=0.5.0"
}

// bar content constants.
const (
	barDim    = "\033[2m"
	barBold   = "\033[1m"
	barReset  = "\033[0m"
	barRed    = "\033[1;31m"
	barGreen  = "\033[32m"
	barYellow = "\033[33m"
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

// SetVersionStatus sets the version indicator and triggers a redraw.
func (sb *StatusBar) SetVersionStatus(status VersionStatus, msg string) {
	sb.mu.Lock()
	sb.versionStatus = status
	sb.versionMsg = msg
	sb.mu.Unlock()
	sb.Redraw()
}

// SetVersionUpdateIfOK sets VersionUpdate only if the current status is VersionOK.
// This prevents a background update check from overwriting a more important status
// like VersionMismatch or VersionDev.
func (sb *StatusBar) SetVersionUpdateIfOK(msg string) bool {
	sb.mu.Lock()
	if sb.versionStatus != VersionOK {
		sb.mu.Unlock()
		return false
	}
	sb.versionStatus = VersionUpdate
	sb.versionMsg = msg
	sb.mu.Unlock()
	sb.Redraw()
	return true
}

// buildBarContent returns the bar string with a colored emoji and status indicators.
func (sb *StatusBar) buildBarContent(width int) string {
	sb.mu.Lock()
	es := sb.errorState
	vs := sb.versionStatus
	vmsg := sb.versionMsg
	sb.mu.Unlock()

	hasErrors := es != nil && es.HasErrors()

	// Emoji color priority: red (errors) > yellow (dev/mismatch/update) > green (ok).
	var hamr string
	switch {
	case hasErrors:
		hamr = "  " + barRed + "🔨" + barReset
	case vs == VersionDev || vs == VersionMismatch || vs == VersionUpdate:
		hamr = "  " + barYellow + "🔨" + barReset
	default:
		hamr = "  " + barGreen + "🔨" + barReset
	}
	base := hamr + barKeys
	used := barBaseVisibleLen // visible chars consumed so far

	// Append status indicators right-to-left priority: ERR first, then VER.
	var suffix string

	if hasErrors {
		names := es.RuleNames()
		errPrefix := "  " + barRed + "ERR" + barReset + barDim + " "
		errPrefixVisibleLen := 6 // "  ERR "

		available := width - used - errPrefixVisibleLen
		if available < 3 {
			suffix += "  " + barRed + "ERR" + barReset
			used += 5 // "  ERR"
		} else {
			joined := strings.Join(names, ", ")
			if len(joined) > available {
				if available <= 3 {
					joined = joined[:available]
				} else {
					joined = joined[:available-1] + "…"
				}
			}
			suffix += errPrefix + joined + barReset
			used += errPrefixVisibleLen + len(joined)
		}
	}

	switch {
	case vs == VersionMismatch && vmsg != "":
		verTag := "  " + barYellow + "VER" + barReset + barDim + " " + vmsg + barReset
		verVisibleLen := 6 + len(vmsg) // "  VER " + msg
		if used+verVisibleLen <= width {
			suffix += verTag
		} else if used+5 <= width {
			suffix += "  " + barYellow + "VER" + barReset
		}
	case vs == VersionUpdate && vmsg != "":
		updTag := "  " + barYellow + "UPD" + barReset + barDim + " " + vmsg + barReset
		updVisibleLen := 6 + len(vmsg) // "  UPD " + msg
		if used+updVisibleLen <= width {
			suffix += updTag
		} else if used+5 <= width {
			suffix += "  " + barYellow + "UPD" + barReset
		}
	case vs == VersionDev:
		if used+5 <= width {
			suffix += "  " + barYellow + "DEV" + barReset
		}
	}

	return base + suffix
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
