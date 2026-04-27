package tui

import (
	"testing"

	"github.com/FyrmForge/hamr/internal/devserver"
)

func dc(name string) devserver.DockerCompose {
	return devserver.DockerCompose{Name: name, File: name + ".yaml"}
}

func TestWipe_OpenSingleEntrySkipsSelection(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra")})
	if w.stage != wipeConfirming {
		t.Fatalf("single entry should jump to confirm stage, got stage=%d", w.stage)
	}
	if w.pickedIx != 0 {
		t.Fatalf("single entry should auto-pick index 0, got %d", w.pickedIx)
	}
}

func TestWipe_OpenMultipleEntriesRequiresPick(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra"), dc("stripe")})
	if w.stage != wipeSelecting {
		t.Fatalf("multi entry should require selection, got stage=%d", w.stage)
	}
	if w.pickedIx != -1 {
		t.Fatalf("multi entry should not pre-pick, got %d", w.pickedIx)
	}
}

func TestWipe_OpenEmptyListClosed(t *testing.T) {
	var w wipeState
	w.open(nil)
	if w.stage != wipeClosed {
		t.Fatalf("empty list must not open the modal, got stage=%d", w.stage)
	}
}

func TestWipe_DigitPicksEntryAndAdvancesToConfirm(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra"), dc("stripe"), dc("redis")})

	d := w.handleKey("2")
	if d.trigger || d.closed {
		t.Fatalf("picking should not trigger or close yet, got %+v", d)
	}
	if w.stage != wipeConfirming {
		t.Fatalf("after pick stage should be confirming, got %d", w.stage)
	}
	if w.pickedIx != 1 {
		t.Fatalf("digit '2' should pick index 1, got %d", w.pickedIx)
	}
}

func TestWipe_OutOfRangeDigitCancels(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra"), dc("stripe")})

	d := w.handleKey("9")
	if !d.closed {
		t.Fatal("digit beyond list should cancel the modal")
	}
	if w.stage != wipeClosed {
		t.Fatalf("modal should be closed after invalid pick, got stage=%d", w.stage)
	}
}

func TestWipe_NonDigitDuringSelectingCancels(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra"), dc("stripe")})

	d := w.handleKey("r")
	if !d.closed {
		t.Fatal("non-digit during selecting should cancel")
	}
	if w.stage != wipeClosed {
		t.Fatalf("modal should be closed, got stage=%d", w.stage)
	}
}

func TestWipe_ConfirmYTriggers(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra")})

	d := w.handleKey("y")
	if !d.trigger {
		t.Fatal("y at confirm should trigger the wipe")
	}
	if d.triggerIx != 0 {
		t.Fatalf("trigger index should match the picked entry, got %d", d.triggerIx)
	}
	if w.stage != wipeRunning {
		t.Fatalf("after trigger stage should be running, got %d", w.stage)
	}
}

func TestWipe_ConfirmCapitalYTriggers(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra")})

	d := w.handleKey("Y")
	if !d.trigger {
		t.Fatal("uppercase Y must also confirm — both shift states should work")
	}
}

func TestWipe_ConfirmAnyOtherKeyCancels(t *testing.T) {
	for _, key := range []string{"n", "N", "esc", "q", "r", " "} {
		var w wipeState
		w.open([]devserver.DockerCompose{dc("infra")})
		d := w.handleKey(key)
		if !d.closed {
			t.Fatalf("key %q at confirm stage must cancel", key)
		}
		if d.trigger {
			t.Fatalf("key %q must not trigger wipe", key)
		}
	}
}

func TestWipe_RunningSwallowsKeysUntilFinish(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra")})
	if d := w.handleKey("y"); !d.trigger {
		t.Fatal("setup: y should trigger")
	}

	// Any keypress during running should be a no-op.
	for _, key := range []string{"y", "n", "r", "q"} {
		d := w.handleKey(key)
		if d.trigger || d.closed {
			t.Fatalf("key %q during running must not transition, got %+v", key, d)
		}
	}
	if w.stage != wipeRunning {
		t.Fatalf("stage must remain running until finish() is called, got %d", w.stage)
	}

	w.finish()
	if w.stage != wipeClosed {
		t.Fatalf("finish must close the modal, got stage=%d", w.stage)
	}
	if w.active() {
		t.Fatal("active() must be false after finish")
	}
}

func TestWipe_FullFlow_MultipleEntries(t *testing.T) {
	var w wipeState
	w.open([]devserver.DockerCompose{dc("infra"), dc("stripe")})

	if d := w.handleKey("2"); d.trigger || d.closed {
		t.Fatalf("pick should not finalise, got %+v", d)
	}
	d := w.handleKey("y")
	if !d.trigger || d.triggerIx != 1 {
		t.Fatalf("confirm should trigger index 1, got %+v", d)
	}
	w.finish()
	if w.active() {
		t.Fatal("modal should be closed after finish")
	}
}
