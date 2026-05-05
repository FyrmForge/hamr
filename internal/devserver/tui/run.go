package tui

import (
	"github.com/sahilm/fuzzy"
)

// runStage tracks where in the run-modal flow the user is.
type runStage int

const (
	runClosed   runStage = iota // modal not visible
	runOverlay                  // fuzzy palette open; awaiting selection
	runRunning                  // target executing; floating box shows it
	runFinished                 // target done; box stays until any key
)

// runState is the pure state machine for the make-target runner. No I/O —
// keys and a target list go in, transitions and a "trigger" decision come
// out. Spawning the actual `make <target>` lives in the bubbletea model.
type runState struct {
	stage   runStage
	targets []string // full list in Makefile order
	query   string
	cursor  int // index into filtered() results

	// running/finished context.
	running   string // target name currently executing or just finished
	exitCode  int
	failed    bool
	failedMsg string // populated when start failed before exec (e.g. cmd not found)
}

// runDecision encodes the outcome of feeding a key into the modal.
type runDecision struct {
	// trigger is set when the user has confirmed a target and the model
	// should now dispatch `make <target>` in a goroutine.
	trigger    bool
	triggerTgt string
	// cancel is set when the running target should be SIGINT'd.
	cancel bool
	// closed is set when the modal should disappear (esc, any-key dismiss).
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

// active reports whether any modal surface is currently visible.
func (r *runState) active() bool { return r.stage != runClosed }

// overlayActive reports whether the fuzzy palette is open.
func (r *runState) overlayActive() bool { return r.stage == runOverlay }

// runningActive reports whether the "running" box is visible (running or
// finished states both render the box).
func (r *runState) runningActive() bool {
	return r.stage == runRunning || r.stage == runFinished
}

// markRunning transitions the state machine to runRunning for the named
// target. Caller is responsible for actually launching the process.
func (r *runState) markRunning(target string) {
	r.stage = runRunning
	r.running = target
	r.exitCode = 0
	r.failed = false
	r.failedMsg = ""
}

// markFinished transitions to runFinished with the given exit info. The
// "running" box stays on screen until the user dismisses with any key.
func (r *runState) markFinished(exitCode int, failed bool, msg string) {
	r.stage = runFinished
	r.exitCode = exitCode
	r.failed = failed
	r.failedMsg = msg
}

// close resets the state machine to runClosed, clearing transient state.
func (r *runState) close() {
	r.stage = runClosed
	r.query = ""
	r.cursor = 0
	r.running = ""
	r.exitCode = 0
	r.failed = false
	r.failedMsg = ""
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
		return runDecision{trigger: true, triggerTgt: target}
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

// handleRunningKey is called while a target is executing. Only `q`
// cancels; everything else is swallowed so the user can't accidentally
// quit the TUI mid-run.
func (r *runState) handleRunningKey(key string) runDecision {
	if r.stage != runRunning {
		return runDecision{}
	}
	if key == "q" {
		return runDecision{cancel: true}
	}
	return runDecision{}
}

// handleFinishedKey is called while the post-run box is shown. Any key
// dismisses it.
func (r *runState) handleFinishedKey(_ string) runDecision {
	if r.stage != runFinished {
		return runDecision{}
	}
	r.close()
	return runDecision{closed: true}
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
