package tui

import "testing"

func TestHelp_StartsClosed(t *testing.T) {
	var h helpState
	if h.active() {
		t.Fatal("zero value must be closed")
	}
}

func TestHelp_ToggleOpensAndCloses(t *testing.T) {
	var h helpState
	h.toggle()
	if !h.active() {
		t.Fatal("first toggle should open")
	}
	h.toggle()
	if h.active() {
		t.Fatal("second toggle should close")
	}
}

func TestHelp_AnyKeyClosesAndReportsClosed(t *testing.T) {
	for _, key := range []string{"q", "?", "esc", "enter", "x", "ctrl+c"} {
		var h helpState
		h.open = true
		d := h.handleKey(key)
		if !d.closed {
			t.Fatalf("key %q must report decision.closed=true", key)
		}
		if h.active() {
			t.Fatalf("modal should be closed after key %q", key)
		}
	}
}

func TestHelp_CloseExplicit(t *testing.T) {
	var h helpState
	h.open = true
	h.close()
	if h.active() {
		t.Fatal("close() must shut the modal")
	}
}

func TestHelp_EntriesIncludeAllHotkeys(t *testing.T) {
	// Sanity check that the help table covers every keybind the model
	// dispatches in handleKey. If we add a binding without listing it
	// here, this test fails — keeping the help table honest.
	wantKeys := []string{"r", "o", "c", "d", "/", "f", "?", "esc"}
	have := map[string]bool{}
	for _, e := range helpEntries {
		have[e.keys] = true
	}
	for _, k := range wantKeys {
		if !have[k] {
			t.Errorf("help table is missing entry for key %q", k)
		}
	}
	// Combined rows.
	if !have["q / Ctrl+C"] {
		t.Error("help table is missing the quit row")
	}
	if !have["Tab / Shift+Tab"] {
		t.Error("help table is missing the tab cycle row")
	}
	if !have["n / N"] {
		t.Error("help table is missing the search-navigation row")
	}
}
