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
	if !r.active() {
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

// TestRunState_EnterClosesOverlay guards the whole point of the palette:
// confirming a target dismisses it immediately instead of locking the TUI
// behind a "running" box — the status bar and the hamr tab report the run.
func TestRunState_EnterClosesOverlay(t *testing.T) {
	r := &runState{}
	r.openOverlay([]string{"build", "test"})
	d := r.handleOverlayKey("enter", 0)
	if !d.trigger || !d.closed {
		t.Fatalf("decision=%+v want trigger+closed", d)
	}
	if r.active() {
		t.Fatal("palette still visible after confirming a target")
	}
	if r.query != "" || r.cursor != 0 {
		t.Fatalf("transient state not cleared: query=%q cursor=%d", r.query, r.cursor)
	}
}
