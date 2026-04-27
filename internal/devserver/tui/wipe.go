package tui

import "github.com/FyrmForge/hamr/internal/devserver"

// wipeStage tracks where in the modal flow the user is.
type wipeStage int

const (
	wipeClosed     wipeStage = iota // modal not visible
	wipeSelecting                   // 2+ entries; awaiting digit pick
	wipeConfirming                  // entry chosen; awaiting y/N
	wipeRunning                     // wipe in flight; modal shows progress
)

// wipeState is the pure state machine for the wipe modal. No I/O — keys
// and a compose list go in, transitions and a "trigger" decision come out.
// Running the actual docker compose down -v lives in the bubbletea model
// (it spawns a goroutine that calls DevActions.DockerWipe).
type wipeState struct {
	stage    wipeStage
	composes []devserver.DockerCompose
	pickedIx int    // index into composes once chosen
	status   string // optional status text shown while running
}

// wipeDecision encodes the outcome of feeding a key into the modal.
type wipeDecision struct {
	// trigger is set when the user has confirmed and the model should now
	// dispatch the wipe in a goroutine. Index into wipeState.composes.
	trigger     bool
	triggerIx   int
	// closed is set when the modal should disappear (cancel, finish).
	closed bool
}

// open initialises the modal for the given compose list. A single entry
// jumps straight to the confirm stage; otherwise the user picks a digit.
func (w *wipeState) open(list []devserver.DockerCompose) {
	w.composes = list
	w.status = ""
	switch len(list) {
	case 0:
		// Nothing to wipe — caller should not have opened.
		w.stage = wipeClosed
	case 1:
		w.pickedIx = 0
		w.stage = wipeConfirming
	default:
		w.pickedIx = -1
		w.stage = wipeSelecting
	}
}

// handleKey advances the state machine in response to a key string (as
// produced by tea.KeyMsg.String()). Returns the decision the model should
// act on. Unknown keys in selecting/confirming cancel the modal — keeping
// the rule simple ("any key not in the prompt cancels") avoids a stuck
// modal swallowing the user's next attempt at r/o/q.
func (w *wipeState) handleKey(key string) wipeDecision {
	switch w.stage {
	case wipeSelecting:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			ix := int(key[0] - '1')
			if ix < len(w.composes) {
				w.pickedIx = ix
				w.stage = wipeConfirming
				return wipeDecision{}
			}
		}
		w.stage = wipeClosed
		return wipeDecision{closed: true}

	case wipeConfirming:
		switch key {
		case "y", "Y":
			ix := w.pickedIx
			w.stage = wipeRunning
			w.status = "wiping " + w.composes[ix].Name + "..."
			return wipeDecision{trigger: true, triggerIx: ix}
		default:
			w.stage = wipeClosed
			return wipeDecision{closed: true}
		}

	case wipeRunning:
		// Keys are ignored while a wipe is in flight; the goroutine that
		// dispatched DockerWipe will close the modal via finish().
		return wipeDecision{}
	}
	return wipeDecision{}
}

// finish marks an in-flight wipe as complete and closes the modal.
func (w *wipeState) finish() {
	w.stage = wipeClosed
	w.status = ""
}

// active reports whether the modal is currently visible.
func (w *wipeState) active() bool { return w.stage != wipeClosed }
