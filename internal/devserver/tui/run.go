package tui

import (
	"github.com/sahilm/fuzzy"
)

// runStage tracks where in the run-modal flow the user is.
type runStage int

const (
	runClosed  runStage = iota // modal not visible
	runOverlay                 // fuzzy palette open; awaiting selection
)

// runState is the pure state machine for the make-target runner. No I/O —
// keys and a target list go in, transitions and a "trigger" decision come
// out. Dispatching the actual `make <target>` lives in the bubbletea model.
//
// The palette closes the moment a target is confirmed: the run itself is
// reported by the status bar (a spinner plus `make <target>`, driven by the
// dev server's event bus) and its output streams into the hamr tab, so there
// is nothing left for a modal to show and no reason to lock the TUI while it
// runs.
type runState struct {
	stage   runStage
	targets []string // full list in Makefile order
	query   string
	cursor  int // index into filtered() results
}

// runDecision encodes the outcome of feeding a key into the modal.
type runDecision struct {
	// trigger is set when the user has confirmed a target and the model
	// should now dispatch `make <target>`.
	trigger    bool
	triggerTgt string
	// closed is set when the modal should disappear (esc).
	closed bool
}

// openOverlay starts a fresh fuzzy-palette session over the given targets.
// An empty list collapses straight to closed — caller should not have
// opened.
func (r *runState) openOverlay(targets []string) {
	if len(targets) == 0 {
		r.stage = runClosed
		return
	}
	r.targets = targets
	r.query = ""
	r.cursor = 0
	r.stage = runOverlay
}

// active reports whether the palette is currently visible.
func (r *runState) active() bool { return r.stage == runOverlay }

// close resets the state machine to runClosed, clearing transient state.
func (r *runState) close() {
	r.stage = runClosed
	r.query = ""
	r.cursor = 0
}

// handleOverlayKey advances the palette in response to a key. Printable
// runes append to the query (cursor resets to 0); backspace deletes;
// up/down move; enter triggers; esc closes.
func (r *runState) handleOverlayKey(key string, printable rune) runDecision {
	if r.stage != runOverlay {
		return runDecision{}
	}
	switch key {
	case "esc":
		r.close()
		return runDecision{closed: true}
	case "enter":
		filtered := r.filtered()
		if len(filtered) == 0 {
			return runDecision{}
		}
		if r.cursor < 0 || r.cursor >= len(filtered) {
			return runDecision{}
		}
		target := filtered[r.cursor]
		r.close()
		return runDecision{trigger: true, triggerTgt: target, closed: true}
	case "up":
		if r.cursor > 0 {
			r.cursor--
		}
		return runDecision{}
	case "down":
		if r.cursor < len(r.filtered())-1 {
			r.cursor++
		}
		return runDecision{}
	case "backspace":
		if len(r.query) > 0 {
			// Strip last rune (UTF-8 safe).
			q := []rune(r.query)
			r.query = string(q[:len(q)-1])
			r.cursor = 0
		}
		return runDecision{}
	}
	if printable != 0 {
		r.query += string(printable)
		r.cursor = 0
	}
	return runDecision{}
}

// filtered returns the targets matching the current query, ranked by
// fuzzy score (best first). With an empty query, the original Makefile
// order is preserved so the palette opens to the same view every time.
func (r *runState) filtered() []string {
	if r.query == "" {
		out := make([]string, len(r.targets))
		copy(out, r.targets)
		return out
	}
	matches := fuzzy.Find(r.query, r.targets)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Str)
	}
	return out
}
