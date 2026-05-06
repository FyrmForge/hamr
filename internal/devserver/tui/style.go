// Package tui implements the experimental bubbletea-based dev runtime
// (hamr dev --tui). The CLI counterpart lives in internal/devserver and
// stays the default until the TUI reaches feature parity.
package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorDim    = lipgloss.AdaptiveColor{Light: "#777777", Dark: "#888888"}
	colorAccent = lipgloss.AdaptiveColor{Light: "#1f6feb", Dark: "#58a6ff"}
	colorOK     = lipgloss.Color("#3fb950")
	colorWarn   = lipgloss.Color("#d29922")
	colorErr    = lipgloss.Color("#f85149")

	statusBarBG = lipgloss.Color("#161b22")
	statusBarFG = lipgloss.AdaptiveColor{Light: "#000000", Dark: "#c9d1d9"}

	statusBarStyle = lipgloss.NewStyle().
			Background(statusBarBG).
			Foreground(statusBarFG)

	// Inner styles inherit the bar background so foreground-only overrides
	// don't punch a hole in the bar's fill.
	statusOK    = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorOK).Bold(true)
	statusErr   = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorErr).Bold(true)
	statusWarn  = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorWarn).Bold(true)
	statusDim   = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorDim)
	statusKey   = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorAccent).Bold(true)
	statusLabel = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorDim)
	statusVer   = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorDim)

	// Emoji background lives on the bar; foreground tints the hammer to
	// match the priority colour the legacy ANSI bar uses.
	hammerOK   = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorOK)
	hammerWarn = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorWarn)
	hammerErr  = lipgloss.NewStyle().Background(statusBarBG).Foreground(colorErr)

	// Docker tab decoration: cyan whale, distinct from the hamr accent
	// blue so the active-tab indicator is unambiguous at a glance.
	dockerColor = lipgloss.Color("#0db7ed")
	dockerIcon  = lipgloss.NewStyle().Background(statusBarBG).Foreground(dockerColor)

	// Search highlights — yellow-on-black for ordinary matches, with a
	// brighter "current" style so n/N visibly moves the focused hit.
	searchHighlight = lipgloss.NewStyle().
			Background(lipgloss.Color("#d29922")).
			Foreground(lipgloss.Color("#000000"))
	searchCurrent = lipgloss.NewStyle().
			Background(lipgloss.Color("#f0883e")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true)

	hintBarStyle = lipgloss.NewStyle().
			Background(statusBarBG).
			Foreground(colorDim)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(1, 2)

	modalTitle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			MarginBottom(1)

	modalDanger = lipgloss.NewStyle().Foreground(colorErr).Bold(true)

	// Modal-internal counterparts to statusDim/statusKey. They drop the
	// status-bar background so dim hints inside modals don't render as a
	// dark block against the modal's transparent body.
	modalDim = lipgloss.NewStyle().Foreground(colorDim)
	modalKey = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)
