package tui

import (
	"reflect"
	"testing"
)

func TestRunState_OpenOverlayEmptyClosesImmediately(t *testing.T) {
	r := &runState{}
	r.openOverlay(nil)
	if r.active() {
		t.Fatal("should remain closed with no targets")
	}
}

func TestRunState_OpenOverlayPopulates(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"build", "test", "vet"})
	if !r.overlayActive() {
		t.Fatal("expected overlay active")
	}
	if r.cursor != 0 {
		t.Fatalf("cursor=%d want 0", r.cursor)
	}
	if got, want := r.filtered(), []string{"build", "test", "vet"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered=%v want %v", got, want)
	}
}

func TestRunState_TypingFiltersAndResetsCursor(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"build", "test", "vet", "build-fast"})
	// Move cursor to 2 first.
	r.handleOverlayKey("down", 0)
	r.handleOverlayKey("down", 0)
	if r.cursor != 2 {
		t.Fatalf("cursor=%d want 2", r.cursor)
	}
	r.handleOverlayKey("b", 'b')
	if r.cursor != 0 {
		t.Fatalf("cursor not reset after typing: %d", r.cursor)
	}
	got := r.filtered()
	// fuzzy.Find with "b" should return targets containing 'b' ranked.
	if len(got) == 0 {
		t.Fatal("expected at least one fuzzy match")
	}
	for _, name := range got {
		if name != "build" && name != "build-fast" {
			t.Fatalf("unexpected match for query 'b': %q", name)
		}
	}
}

func TestRunState_BackspaceDeletesLastRune(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"build"})
	r.handleOverlayKey("b", 'b')
	r.handleOverlayKey("u", 'u')
	r.handleOverlayKey("backspace", 0)
	if r.query != "b" {
		t.Fatalf("query=%q want 'b'", r.query)
	}
}

func TestRunState_UpDownClampToFiltered(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"a", "b", "c"})
	// Up from 0 stays at 0.
	r.handleOverlayKey("up", 0)
	if r.cursor != 0 {
		t.Fatalf("cursor=%d want 0", r.cursor)
	}
	// Down past end clamps.
	r.handleOverlayKey("down", 0)
	r.handleOverlayKey("down", 0)
	r.handleOverlayKey("down", 0)
	r.handleOverlayKey("down", 0)
	if r.cursor != 2 {
		t.Fatalf("cursor=%d want 2", r.cursor)
	}
}

func TestRunState_EnterTriggersSelectedTarget(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"build", "test", "vet"})
	r.handleOverlayKey("down", 0)
	d := r.handleOverlayKey("enter", 0)
	if !d.trigger || d.triggerTgt != "test" {
		t.Fatalf("decision=%+v", d)
	}
}

func TestRunState_EnterOnEmptyFilteredNoOp(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"build"})
	// Type a query that won't match anything.
	r.handleOverlayKey("z", 'z')
	r.handleOverlayKey("z", 'z')
	r.handleOverlayKey("z", 'z')
	d := r.handleOverlayKey("enter", 0)
	if d.trigger {
		t.Fatal("enter should not trigger with no matches")
	}
}

func TestRunState_EscClosesOverlay(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"a"})
	d := r.handleOverlayKey("esc", 0)
	if !d.closed || r.active() {
		t.Fatalf("expected closed; decision=%+v active=%v", d, r.active())
	}
}

func TestRunState_RunningQCancels(t *testing.T) {
	r := &runState{}
	r.markRunning("build")
	if !r.runningActive() {
		t.Fatal("expected runningActive")
	}
	d := r.handleRunningKey("q")
	if !d.cancel {
		t.Fatalf("expected cancel decision; got %+v", d)
	}
}

func TestRunState_RunningOtherKeysIgnored(t *testing.T) {
	r := &runState{}
	r.markRunning("build")
	for _, k := range []string{"r", "m", "enter", "esc", "ctrl+c", "tab", "?"} {
		d := r.handleRunningKey(k)
		if d.cancel || d.closed || d.trigger {
			t.Fatalf("key %q produced non-empty decision %+v", k, d)
		}
		if r.stage != runRunning {
			t.Fatalf("key %q changed stage", k)
		}
	}
}

func TestRunState_FinishedAnyKeyDismisses(t *testing.T) {
	r := &runState{}
	r.markRunning("build")
	r.markFinished(0, false, "")
	d := r.handleFinishedKey("x")
	if !d.closed || r.active() {
		t.Fatalf("expected closed; decision=%+v stage=%v", d, r.stage)
	}
}

func TestRunState_FinishedFailedRetainsExitInfo(t *testing.T) {
	r := &runState{}
	r.markRunning("build")
	r.markFinished(2, true, "")
	if !r.failed || r.exitCode != 2 {
		t.Fatalf("failed=%v exit=%d", r.failed, r.exitCode)
	}
	if r.running != "build" {
		t.Fatalf("running=%q want 'build'", r.running)
	}
}

// TestSpinTick_AdvancesWhileRunningAndStops checks the spinner chain is
// self-limiting: it advances a frame and reschedules while the target is
// running, and returns no follow-up command once the run is over — a
// leaked chain would repaint forever after the box is gone.
func TestSpinTick_AdvancesWhileRunningAndStops(t *testing.T) {
	m := &Model{}
	m.run.markRunning("build")

	_, cmd := m.Update(spinTickMsg{})
	if m.spinFrame != 1 {
		t.Fatalf("spinFrame=%d want 1", m.spinFrame)
	}
	if cmd == nil {
		t.Fatal("expected the tick chain to reschedule while running")
	}

	m.run.markFinished(0, false, "")
	_, cmd = m.Update(spinTickMsg{})
	if cmd != nil {
		t.Fatal("tick chain should stop once the run is finished")
	}
	if m.spinFrame != 1 {
		t.Fatalf("spinFrame=%d want 1 (no advance after finish)", m.spinFrame)
	}
}
