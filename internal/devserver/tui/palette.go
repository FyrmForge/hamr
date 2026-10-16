package tui

import (
	"github.com/sahilm/fuzzy"
)

// paletteStage tracks whether the command palette is visible.
type paletteStage int

const (
	paletteClosed paletteStage = iota // modal not visible
	paletteOpen                       // fuzzy palette open; awaiting selection
)

// paletteEntry is one row of the command palette.
type paletteEntry struct {
	name   string // displayed and fuzzy-matched
	key    string // hotkey the entry stands in for (hamr commands); "" for make entries
	target string // make target to run (make entries); "" for hamr commands
}

// paletteState is the pure state machine for the command palette. No I/O —
// keys and an entry list go in, transitions and a "trigger" decision come
// out. Running the chosen entry (replaying its hotkey or dispatching
// `make <target>`) lives in the bubbletea model.
//
// The palette closes the moment an entry is confirmed: a make run is
// reported by the status bar (a spinner plus `make <target>`, driven by the
// dev server's event bus) and its output streams into the hamr tab, so there
// is nothing left for a modal to show and no reason to lock the TUI while it
// runs.
type paletteState struct {
	stage   paletteStage
	entries []paletteEntry // full list in display order
	query   string
	cursor  int // index into filtered() results
}

// paletteDecision encodes the outcome of feeding a key into the modal.
type paletteDecision struct {
	// trigger is set when the user has confirmed an entry and the model
	// should now run it.
	trigger bool
	entry   paletteEntry
	// closed is set when the modal should disappear (esc, or a confirmed entry).
	closed bool
}

// open starts a fresh palette session over the given entries, with query
// pre-typed (cursor 0). An empty list collapses straight to closed — caller
// should not have opened.
func (p *paletteState) open(entries []paletteEntry, query string) {
	if len(entries) == 0 {
		p.stage = paletteClosed
		return
	}
	p.entries = entries
	p.query = query
	p.cursor = 0
	p.stage = paletteOpen
}

// active reports whether the palette is currently visible.
func (p *paletteState) active() bool { return p.stage == paletteOpen }

// close resets the state machine to paletteClosed, clearing transient state.
func (p *paletteState) close() {
	p.stage = paletteClosed
	p.query = ""
	p.cursor = 0
}

// handleKey advances the palette in response to a key. Printable runes
// append to the query (cursor resets to 0); backspace deletes; up/down move;
// enter triggers; esc closes.
func (p *paletteState) handleKey(key string, printable rune) paletteDecision {
	if p.stage != paletteOpen {
		return paletteDecision{}
	}
	switch key {
	case "esc":
		p.close()
		return paletteDecision{closed: true}
	case "enter":
		filtered := p.filtered()
		if p.cursor < 0 || p.cursor >= len(filtered) {
			return paletteDecision{}
		}
		entry := filtered[p.cursor]
		p.close()
		return paletteDecision{trigger: true, entry: entry, closed: true}
	case "up":
		if p.cursor > 0 {
			p.cursor--
		}
		return paletteDecision{}
	case "down":
		if p.cursor < len(p.filtered())-1 {
			p.cursor++
		}
		return paletteDecision{}
	case "backspace":
		if len(p.query) > 0 {
			// Strip last rune (UTF-8 safe).
			q := []rune(p.query)
			p.query = string(q[:len(q)-1])
			p.cursor = 0
		}
		return paletteDecision{}
	}
	if printable != 0 {
		p.query += string(printable)
		p.cursor = 0
	}
	return paletteDecision{}
}

// paletteNames adapts entries to fuzzy.Source so matching runs over the
// displayed names.
type paletteNames []paletteEntry

func (n paletteNames) String(i int) string { return n[i].name }
func (n paletteNames) Len() int            { return len(n) }

// filtered returns the entries matching the current query, ranked by fuzzy
// score (best first). With an empty query, the original order is preserved
// so the palette opens to the same view every time.
func (p *paletteState) filtered() []paletteEntry {
	if p.query == "" {
		out := make([]paletteEntry, len(p.entries))
		copy(out, p.entries)
		return out
	}
	matches := fuzzy.FindFrom(p.query, paletteNames(p.entries))
	out := make([]paletteEntry, 0, len(matches))
	for _, m := range matches {
		out = append(out, p.entries[m.Index])
	}
	return out
}
