package tui

// helpState is the (very) small state machine for the help modal. Only
// two states (open/closed) but expressed as a struct for symmetry with
// wipeState — keeps the model's modal handling uniform.
type helpState struct {
	open bool
}

// helpDecision captures the outcome of a key press while the help modal
// is visible. Anything closes it; navigation keys do not bleed through to
// the viewport (otherwise the user's "?-then-PgDn-to-dismiss" instinct
// would scroll the logs underneath the modal).
type helpDecision struct {
	closed bool
}

func (h *helpState) toggle() {
	h.open = !h.open
}

func (h *helpState) close() {
	h.open = false
}

func (h *helpState) active() bool { return h.open }

// handleKey closes the modal on any key press. Returns a closed=true
// decision so the caller can short-circuit further key handling.
func (h *helpState) handleKey(_ string) helpDecision {
	h.open = false
	return helpDecision{closed: true}
}

// helpEntry is one row of the help table.
type helpEntry struct {
	keys string
	desc string
}

// helpEntries is the canonical list rendered inside the modal. Kept in a
// single place so the hint bar and the help modal can never drift —
// future bindings should be added here and tagged into both surfaces.
var helpEntries = []helpEntry{
	{"r", "rebuild all watch rules"},
	{"o", "open the proxy URL in browser"},
	{"c", "clear the active tab's log buffer"},
	{"d", "wipe a docker compose stack (down -v + up -d)"},
	{"Tab / Shift+Tab", "cycle log tabs (hamr ↔ docker stacks)"},
	{"/", "search the active tab — type live, ↩ commits, esc cancels"},
	{"n / N", "next / previous match (after committing a search)"},
	{"f", "toggle filter view — show only lines containing the search"},
	{"esc", "clear the active search"},
	{"?", "toggle this help"},
	{"q / Ctrl+C", "quit"},
	{"", ""},
	{"↑ / ↓", "scroll log viewport line by line"},
	{"PgUp / PgDn", "scroll log viewport by page"},
	{"↩", "jump back to bottom and resume tailing"},
	{"mouse wheel", "scroll log viewport"},
}
