package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/FyrmForge/hamr/internal/devserver"
)

// entriesOf builds make-style entries (name == target) from bare names.
func entriesOf(names ...string) []paletteEntry {
	out := make([]paletteEntry, len(names))
	for i, n := range names {
		out[i] = paletteEntry{name: n, target: n}
	}
	return out
}

func names(es []paletteEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.name
	}
	return out
}

// withMakefile points the palette at a temp Makefile with the given targets.
func withMakefile(t *testing.T, targets ...string) {
	t.Helper()
	body := ""
	for _, tg := range targets {
		body += tg + ":\n\t@true\n"
	}
	p := filepath.Join(t.TempDir(), "Makefile")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := makefilePath
	makefilePath = p
	t.Cleanup(func() { makefilePath = old })
}

// withoutMakefile points the palette at a path that does not exist, so tests
// don't read the Makefile in the test's working directory.
func withoutMakefile(t *testing.T) {
	t.Helper()
	old := makefilePath
	makefilePath = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { makefilePath = old })
}

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestPalette_MPrefillsMakeAndListsOnlyMake(t *testing.T) {
	withMakefile(t, "build", "db-seed")
	m := NewModel(NewHotkeySource())
	m.handleKey(keyMsg("m"))
	if !m.palette.active() || m.palette.query != "make:" {
		t.Fatalf("active=%v query=%q", m.palette.active(), m.palette.query)
	}
	got := names(m.palette.filtered())
	if want := []string{"make:build", "make:db-seed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered=%v want %v", got, want)
	}
}

func TestPalette_MNoMakefileNoOp(t *testing.T) {
	withoutMakefile(t)
	m := NewModel(NewHotkeySource())
	m.handleKey(keyMsg("m"))
	if m.palette.active() {
		t.Fatal("m must do nothing without a Makefile")
	}
}

func TestPalette_ColonOpensEverything(t *testing.T) {
	withMakefile(t, "build")
	m := NewModel(NewHotkeySource())
	m.handleKey(keyMsg(":"))
	if !m.palette.active() || m.palette.query != "" {
		t.Fatalf("active=%v query=%q", m.palette.active(), m.palette.query)
	}
	got := names(m.palette.filtered())
	if got[0] != "rebuild" || got[len(got)-1] != "make:build" {
		t.Fatalf("entries=%v", got)
	}
}

func TestPalette_GatedEntries(t *testing.T) {
	withMakefile(t, "build")
	has := func(m *Model, name string) bool {
		for _, e := range m.paletteEntries() {
			if e.name == name {
				return true
			}
		}
		return false
	}
	m := NewModel(NewHotkeySource())
	for _, n := range []string{"toggle mcp", "toggle tunnel", "stripe mode"} {
		if has(m, n) {
			t.Fatalf("%q present with its feature off", n)
		}
	}
	m.mcpKnown = true
	m.proxyURL = "http://localhost:3000"
	m.stripeMode = "mock"
	for _, n := range []string{"toggle mcp", "toggle tunnel", "stripe mode"} {
		if !has(m, n) {
			t.Fatalf("%q missing with its feature on", n)
		}
	}
}

// Every hamr command in the palette must be handled by runBinding, so a
// palette entry can't point at a key that does nothing. Send is non-blocking
// (drops when the buffer is full), so replaying all keys can't hang.
func TestPalette_EveryKeyedEntryIsBound(t *testing.T) {
	withoutMakefile(t)
	m := NewModel(NewHotkeySource())
	m.mcpKnown = true
	m.proxyURL = "http://localhost:3000"
	m.stripeMode = "mock"
	for _, e := range m.paletteEntries() {
		if e.key == "" {
			continue
		}
		if _, ok := m.runBinding(e.key); !ok {
			t.Errorf("palette entry %q has key %q that runBinding does not handle", e.name, e.key)
		}
	}
}

// The modal must never be wider than the terminal, however long a name is.
func TestPalette_ViewFitsNarrowTerminal(t *testing.T) {
	withMakefile(t, strings.Repeat("x", 60))
	m := NewModel(NewHotkeySource())
	m.width, m.height = 40, 30
	m.handleKey(keyMsg(":"))
	for _, r := range strings.Repeat("q", 80) {
		m.palette.query += string(r)
	}
	for i, l := range strings.Split(m.paletteView(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Fatalf("line %d is %d cells, terminal is %d: %q", i, w, m.width, l)
		}
	}
}

func TestPalette_SelectingHamrCommandRunsHotkey(t *testing.T) {
	withMakefile(t, "build")
	hk := NewHotkeySource()
	m := NewModel(hk)
	m.proxyURL = "http://localhost:3000"
	m.handleKey(keyMsg(":"))
	for _, r := range "toggle tunnel" {
		m.handleKey(keyMsg(string(r)))
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.palette.active() {
		t.Fatal("palette should close on confirm")
	}
	select {
	case a := <-hk.Actions():
		if a != devserver.HotkeyTunnelToggle {
			t.Fatalf("action=%v want tunnel toggle", a)
		}
	default:
		t.Fatal("no hotkey action sent")
	}
}

func TestPalette_SelectingHelpOpensHelp(t *testing.T) {
	withoutMakefile(t)
	m := NewModel(NewHotkeySource())
	m.handleKey(keyMsg(":"))
	for _, r := range "help" {
		m.handleKey(keyMsg(string(r)))
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.palette.active() || !m.help.active() {
		t.Fatalf("palette=%v help=%v", m.palette.active(), m.help.active())
	}
}

func TestPalette_FuzzyFindsMakeTarget(t *testing.T) {
	withMakefile(t, "build", "db-seed")
	m := NewModel(NewHotkeySource())
	m.handleKey(keyMsg(":"))
	for _, r := range "db-see" {
		m.handleKey(keyMsg(string(r)))
	}
	got := m.palette.filtered()
	if len(got) == 0 || got[0].name != "make:db-seed" || got[0].target != "db-seed" {
		t.Fatalf("filtered=%v", got)
	}
}

func TestPalette_OpenEmptyClosesImmediately(t *testing.T) {
	r := &paletteState{}
	r.open(nil, "")
	if r.active() {
		t.Fatal("should remain closed with no targets")
	}
}

func TestPalette_OpenPopulates(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("build", "test", "vet"), "")
	if !r.active() {
		t.Fatal("expected overlay active")
	}
	if r.cursor != 0 {
		t.Fatalf("cursor=%d want 0", r.cursor)
	}
	if got, want := r.filtered(), []string{"build", "test", "vet"}; !reflect.DeepEqual(names(got), want) {
		t.Fatalf("filtered=%v want %v", got, want)
	}
}

func TestPalette_TypingFiltersAndResetsCursor(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("build", "test", "vet", "build-fast"), "")
	// Move cursor to 2 first.
	r.handleKey("down", 0)
	r.handleKey("down", 0)
	if r.cursor != 2 {
		t.Fatalf("cursor=%d want 2", r.cursor)
	}
	r.handleKey("b", 'b')
	if r.cursor != 0 {
		t.Fatalf("cursor not reset after typing: %d", r.cursor)
	}
	got := r.filtered()
	// fuzzy.Find with "b" should return targets containing 'b' ranked.
	if len(got) == 0 {
		t.Fatal("expected at least one fuzzy match")
	}
	for _, e := range got {
		if e.name != "build" && e.name != "build-fast" {
			t.Fatalf("unexpected match for query 'b': %q", e.name)
		}
	}
}

func TestPalette_BackspaceDeletesLastRune(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("build"), "")
	r.handleKey("b", 'b')
	r.handleKey("u", 'u')
	r.handleKey("backspace", 0)
	if r.query != "b" {
		t.Fatalf("query=%q want 'b'", r.query)
	}
}

func TestPalette_UpDownClampToFiltered(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("a", "b", "c"), "")
	// Up from 0 stays at 0.
	r.handleKey("up", 0)
	if r.cursor != 0 {
		t.Fatalf("cursor=%d want 0", r.cursor)
	}
	// Down past end clamps.
	r.handleKey("down", 0)
	r.handleKey("down", 0)
	r.handleKey("down", 0)
	r.handleKey("down", 0)
	if r.cursor != 2 {
		t.Fatalf("cursor=%d want 2", r.cursor)
	}
}

func TestPalette_EnterTriggersSelectedTarget(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("build", "test", "vet"), "")
	r.handleKey("down", 0)
	d := r.handleKey("enter", 0)
	if !d.trigger || d.entry.target != "test" {
		t.Fatalf("decision=%+v", d)
	}
}

func TestPalette_EnterOnEmptyFilteredNoOp(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("build"), "")
	// Type a query that won't match anything.
	r.handleKey("z", 'z')
	r.handleKey("z", 'z')
	r.handleKey("z", 'z')
	d := r.handleKey("enter", 0)
	if d.trigger {
		t.Fatal("enter should not trigger with no matches")
	}
}

func TestPalette_EscClosesOverlay(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("a"), "")
	d := r.handleKey("esc", 0)
	if !d.closed || r.active() {
		t.Fatalf("expected closed; decision=%+v active=%v", d, r.active())
	}
}

// TestPalette_EnterClosesOverlay guards the whole point of the palette:
// confirming a target dismisses it immediately instead of locking the TUI
// behind a "running" box — the status bar and the hamr tab report the run.
func TestPalette_EnterClosesOverlay(t *testing.T) {
	r := &paletteState{}
	r.open(entriesOf("build", "test"), "")
	d := r.handleKey("enter", 0)
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
