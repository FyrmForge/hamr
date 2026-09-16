package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/charmbracelet/lipgloss"
)

// TestModel_Bars_TruncateOverflowingLeftCluster guards the bar-overflow bug:
// when the left cluster alone (long ERR list, search query) exceeds the
// terminal width, the bar must truncate it rather than overflow and wrap — a
// wrapped bar pushes every row down and corrupts the whole frame.
func TestModel_Bars_TruncateOverflowingLeftCluster(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.width = 30

	// Hint bar: a long ERR rule list in the bottom-right status.
	m.errors = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}
	if w := lipgloss.Width(m.hintBar()); w > m.width {
		t.Fatalf("hint bar width %d exceeds terminal width %d", w, m.width)
	}
	m.errors = nil

	// Status bar: a long proxy URL overflows the width.
	m.proxyURL = "http://" + strings.Repeat("x", 80) + ":3000"
	if w := lipgloss.Width(m.statusBar()); w > m.width {
		t.Fatalf("status bar width %d exceeds terminal width %d", w, m.width)
	}

	// Hint bar: an active search with a long query overflows the left cluster.
	s := m.activeSearch()
	s.open()
	for _, r := range strings.Repeat("q", 80) {
		s.appendRune(r)
	}
	s.commit(m.currentLogs())
	if w := lipgloss.Width(m.hintBar()); w > m.width {
		t.Fatalf("hint bar width %d exceeds terminal width %d", w, m.width)
	}
}

// TestModel_HelpModal_FitsAvailableHeight guards the modal off-by-one: at tight
// terminal heights the rendered help modal must not exceed the available rows
// (m.height-2), or its bottom border is cropped where it overlaps the hint bar.
func TestModel_HelpModal_FitsAvailableHeight(t *testing.T) {
	// h>=12 is the range where the entry-budget logic is meant to size the
	// modal to fit; the uncounted title margin pushes it one row over.
	for h := 12; h <= 24; h++ {
		m := NewModel(NewHotkeySource())
		m.width = 60
		m.height = h
		got := lipgloss.Height(m.helpView())
		if avail := m.availableModalHeight(); got > avail {
			t.Fatalf("height=%d: help modal renders %d rows, exceeds available %d (bottom border cropped)", h, got, avail)
		}
	}
}

// TestModel_SystemStatus covers the status-bar ticker. The whole point of the
// indicator is that it reflects what the dev server is doing regardless of who
// triggered it, so each state is driven through the same broker events the
// runtime forwards.
func TestModel_SystemStatus(t *testing.T) {
	ev := func(m *Model, t string, data string) {
		m.applyBrokerEvent(devserver.SSEEvent{Type: t, Data: data})
	}

	t.Run("idle", func(t *testing.T) {
		m := NewModel(NewHotkeySource())
		if got := m.systemStatus(); !strings.Contains(got, "OK") {
			t.Fatalf("idle status = %q, want OK", got)
		}
	})

	t.Run("building", func(t *testing.T) {
		m := NewModel(NewHotkeySource())
		ev(m, devserver.EvBuilding, "site")
		if got := m.systemStatus(); !strings.Contains(got, "building site") {
			t.Fatalf("status = %q, want building site", got)
		}
		ev(m, devserver.EvBuildOK, "site")
		if got := m.systemStatus(); !strings.Contains(got, "OK") {
			t.Fatalf("status after build_ok = %q, want OK", got)
		}
	})

	t.Run("compose start", func(t *testing.T) {
		// Pulling images is the slowest part of a cold start and used to show
		// nothing at all.
		m := NewModel(NewHotkeySource())
		ev(m, devserver.EvComposeUp, "postgres")
		if got := m.systemStatus(); !strings.Contains(got, "starting postgres") {
			t.Fatalf("status = %q, want starting postgres", got)
		}
		ev(m, devserver.EvComposeOK, "postgres")
		if got := m.systemStatus(); !strings.Contains(got, "OK") {
			t.Fatalf("status after compose_ok = %q, want OK", got)
		}
	})

	t.Run("build error clears only the rule that failed", func(t *testing.T) {
		// The payload is JSON; clearing every entry on it would blank the
		// sibling build that is still perfectly fine.
		m := NewModel(NewHotkeySource())
		ev(m, devserver.EvBuilding, "site")
		ev(m, devserver.EvBuilding, "css")
		ev(m, devserver.EvBuildError, `{"rule":"css","output":"boom"}`)
		if got := m.active; !reflect.DeepEqual(got, []string{"building site"}) {
			t.Fatalf("active = %v, want only building site", got)
		}
	})

	t.Run("concurrent work all shows", func(t *testing.T) {
		// Another target finishing must not blank the indicator for the one
		// still running, and everything in flight shares the ticker.
		m := NewModel(NewHotkeySource())
		ev(m, devserver.EvMakeStart, "slow")
		ev(m, devserver.EvMakeDone, "other 0")
		ev(m, devserver.EvBuilding, "site")
		got := m.systemStatus()
		if !strings.Contains(got, "make slow") || !strings.Contains(got, "building site") {
			t.Fatalf("status = %q, want both items", got)
		}
	})

	t.Run("duplicate start is not listed twice", func(t *testing.T) {
		m := NewModel(NewHotkeySource())
		ev(m, devserver.EvBuilding, "site")
		ev(m, devserver.EvBuilding, "site")
		if len(m.active) != 1 {
			t.Fatalf("active = %v, want one entry", m.active)
		}
	})

	t.Run("restarting outranks everything", func(t *testing.T) {
		m := NewModel(NewHotkeySource())
		ev(m, devserver.EvBuilding, "site")
		ev(m, devserver.EvRestarting, "")
		got := m.systemStatus()
		if !strings.Contains(got, "restarting") || strings.Contains(got, "building") {
			t.Fatalf("status = %q, want restarting alone", got)
		}
		// The next run coming up clears it, and the work that died with the
		// old run goes with it.
		m.Update(actionsReadyMsg{})
		if got := m.systemStatus(); !strings.Contains(got, "OK") {
			t.Fatalf("status after actions ready = %q, want OK", got)
		}
	})

	t.Run("errors share the ticker with live work", func(t *testing.T) {
		// A stuck error used to outrank everything, so a rebuild triggered to
		// fix it was invisible for as long as it ran.
		m := NewModel(NewHotkeySource())
		m.errors = []string{"site"}
		if got := m.systemStatus(); !strings.Contains(got, "ERR: site") {
			t.Fatalf("status = %q, want ERR: site", got)
		}
		ev(m, devserver.EvMakeStart, "css")
		got := m.systemStatus()
		if !strings.Contains(got, "ERR: site") || !strings.Contains(got, "make css") {
			t.Fatalf("status = %q, want the error and the running make", got)
		}
	})
}

// TestMarquee covers the ticker window: content that fits never moves, content
// that overflows scrolls one cell per step and wraps around without a seam.
func TestMarquee(t *testing.T) {
	t.Run("fits, so it does not scroll", func(t *testing.T) {
		for _, off := range []int{0, 1, 7} {
			if got := marquee([]string{"abc"}, 10, off); got != "abc" {
				t.Fatalf("offset %d = %q, want abc unmoved", off, got)
			}
		}
	})

	t.Run("overflow scrolls one cell per step", func(t *testing.T) {
		items := []string{"aaaa", "bbbb"}
		first := marquee(items, 6, 0)
		if first != "aaaa  " {
			t.Fatalf("offset 0 = %q", first)
		}
		if got := marquee(items, 6, 1); got != "aaa  •" {
			t.Fatalf("offset 1 = %q, want a one-cell shift", got)
		}
	})

	t.Run("wraps around to the start", func(t *testing.T) {
		items := []string{"aaaa", "bbbb"}
		// strip is items + one trailing separator; a full lap returns to the
		// original window, so the loop has no visible seam.
		strip := len([]rune(strings.Join(items, tickerSep) + tickerSep))
		if got, want := marquee(items, 6, strip), marquee(items, 6, 0); got != want {
			t.Fatalf("after a full lap = %q, want %q", got, want)
		}
	})

	t.Run("empty and zero width are safe", func(t *testing.T) {
		if got := marquee(nil, 10, 0); got != "" {
			t.Fatalf("empty items = %q", got)
		}
		if got := marquee([]string{"a"}, 0, 0); got != "" {
			t.Fatalf("zero width = %q", got)
		}
	})
}

// TestStatusBar_FitsNarrowTerminal guards both bars against the ticker: the
// status slot (bottom-right of the hint bar) is the variable-width piece with
// a real appetite. Overflowing would wrap a bar onto a second line, and the
// status must survive narrowing — the hints truncate instead.
func TestStatusBar_FitsNarrowTerminal(t *testing.T) {
	for _, width := range []int{40, 60, 100} {
		m := NewModel(NewHotkeySource())
		m.width = width
		m.proxyURL = "http://localhost:8080"
		for _, rule := range []string{"site", "css", "templ", "sqlc"} {
			m.applyBrokerEvent(devserver.SSEEvent{Type: devserver.EvBuilding, Data: rule})
		}
		if got := lipgloss.Width(m.statusBar()); got != width {
			t.Fatalf("width %d: status bar rendered %d cells", width, got)
		}
		if got := lipgloss.Width(m.hintBar()); got != width {
			t.Fatalf("width %d: hint bar rendered %d cells", width, got)
		}
		if bar := m.hintBar(); !strings.Contains(bar, "building") {
			t.Fatalf("width %d: status dropped from hint bar: %q", width, bar)
		}
	}
}

// TestStatusBar_TunnelURLReplacesProxyURL: while the tunnel is on its public
// URL takes the proxy URL's slot (it's what `o` opens); off restores it.
func TestStatusBar_TunnelURLReplacesProxyURL(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.width = 200
	m.proxyURL = "http://localhost:3000"
	m.applyBrokerEvent(devserver.SSEEvent{Type: devserver.EvTunnelStart, Data: "starting"})
	if bar := m.hintBar(); !strings.Contains(bar, "tunnel starting") {
		t.Fatalf("tunnel starting: %q", bar)
	}
	m.applyBrokerEvent(devserver.SSEEvent{Type: devserver.EvTunnelUp, Data: "https://x.trycloudflare.com"})
	bar := m.statusBar()
	if !strings.Contains(bar, "https://x.trycloudflare.com") || strings.Contains(bar, "localhost:3000") || strings.Contains(m.hintBar(), "tunnel starting") {
		t.Fatalf("tunnel on: %q", bar)
	}
	m.applyBrokerEvent(devserver.SSEEvent{Type: devserver.EvTunnelDown})
	if bar := m.statusBar(); !strings.Contains(bar, "localhost:3000") || strings.Contains(bar, "trycloudflare") {
		t.Fatalf("tunnel off: %q", bar)
	}
}

// TestSpinTick_RunsOnlyWhileBusy guards the tick chain: it starts when the
// status bar enters a busy state, keeps rescheduling while it stays busy, and
// stops dead once idle — a leaked chain would repaint the bar forever.
func TestSpinTick_RunsOnlyWhileBusy(t *testing.T) {
	m := &Model{}

	// Idle: a broker event that leaves nothing in flight starts no chain.
	_, cmd := m.Update(brokerEventMsg{evt: devserver.SSEEvent{Type: devserver.EvBuildOK, Data: "site"}})
	if cmd != nil || m.spinning {
		t.Fatal("chain started while idle")
	}

	_, cmd = m.Update(brokerEventMsg{evt: devserver.SSEEvent{Type: devserver.EvBuilding, Data: "site"}})
	if cmd == nil || !m.spinning {
		t.Fatal("chain did not start when the build began")
	}

	// A second busy event must not start a competing chain.
	_, cmd = m.Update(brokerEventMsg{evt: devserver.SSEEvent{Type: devserver.EvMakeStart, Data: "db-refresh"}})
	if cmd != nil {
		t.Fatal("second busy event started a duplicate chain")
	}

	_, cmd = m.Update(spinTickMsg{})
	if cmd == nil {
		t.Fatal("chain should reschedule while busy")
	}
	if m.spinFrame != 1 {
		t.Fatalf("spinFrame=%d want 1", m.spinFrame)
	}
	if got := m.systemStatus(); !strings.Contains(got, spinFrames[1]+" building site") {
		t.Fatalf("status = %q, want the current spinner frame", got)
	}

	// Everything finishes: the next tick ends the chain.
	m.Update(brokerEventMsg{evt: devserver.SSEEvent{Type: devserver.EvMakeDone, Data: "db-refresh 0"}})
	m.Update(brokerEventMsg{evt: devserver.SSEEvent{Type: devserver.EvBuildOK, Data: "site"}})
	_, cmd = m.Update(spinTickMsg{})
	if cmd != nil || m.spinning {
		t.Fatal("chain should stop once idle")
	}
	if m.spinFrame != 1 {
		t.Fatalf("spinFrame=%d want 1 (no advance after idle)", m.spinFrame)
	}
}

// A restart clears the busy list without a tick passing through, so the
// spinning flag has to be cleared with it. Latching it true would leave
// spinIfBusy refusing to start a chain for the rest of the session — no
// spinner and no scrolling, forever.
func TestActionsReady_ReleasesSpinnerChain(t *testing.T) {
	m := NewModel(NewHotkeySource())
	m.Update(brokerEventMsg{evt: devserver.SSEEvent{Type: devserver.EvBuilding, Data: "site"}})
	if !m.spinning {
		t.Fatal("chain should be running during the build")
	}

	m.Update(actionsReadyMsg{})
	if m.spinning {
		t.Fatal("spinning latched true after the run was replaced")
	}

	_, cmd := m.Update(brokerEventMsg{evt: devserver.SSEEvent{Type: devserver.EvBuilding, Data: "site"}})
	if cmd == nil {
		t.Fatal("chain must restart for the new run's first build")
	}
}
