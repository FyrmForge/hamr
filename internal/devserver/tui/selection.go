package tui

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/atotto/clipboard"
)

// selectionState tracks the active line selection used by mouse
// click-to-select / `y` copy. Lines are stored as logical buffer
// indices into whichever tab's log buffer was active when the
// selection was made; eviction in the bounded buffer shifts them
// down via shiftEvicted and drops anything that falls off the start.
//
// A single state covers the whole TUI (not per-tab): switching tabs
// clears the selection because the stored indices wouldn't be
// meaningful against a different buffer.
type selectionState struct {
	anchor int          // logical line index of the most recent plain/range click
	lines  map[int]bool // set of selected logical line indices
}

func (s *selectionState) hasAny() bool { return s != nil && len(s.lines) > 0 }
func (s *selectionState) count() int   { return len(s.lines) }

// clickPlain replaces the existing selection with a single line and
// resets the anchor — the conventional "click drops everything else"
// behaviour.
func (s *selectionState) clickPlain(line int) {
	s.lines = map[int]bool{line: true}
	s.anchor = line
}

// clickToggle flips one line's membership without touching the rest
// (ctrl-click). The anchor follows the click so a subsequent shift-
// click extends from this point — mirrors VS Code/Finder semantics.
func (s *selectionState) clickToggle(line int) {
	if s.lines == nil {
		s.lines = make(map[int]bool)
	}
	if s.lines[line] {
		delete(s.lines, line)
	} else {
		s.lines[line] = true
	}
	s.anchor = line
}

// clickRange replaces the selection with the inclusive anchor→line
// range (shift-click). When no anchor exists this collapses to a
// plain click. The anchor is left where it is so repeated shift-clicks
// extend from the same origin instead of the most recent end.
// visible, when non-nil, restricts the selection to those buffer indices.
// In filter view only matching lines are shown, so the hidden non-matching
// indices between two visible rows must not be selected — otherwise `y` would
// copy lines the user never saw. nil means "no filter, select the whole range".
func (s *selectionState) clickRange(line int, visible map[int]bool) {
	a := s.anchor
	if !s.hasAny() {
		a = line
		s.anchor = line
	}
	lo, hi := a, line
	if lo > hi {
		lo, hi = hi, lo
	}
	s.lines = make(map[int]bool, hi-lo+1)
	for i := lo; i <= hi; i++ {
		if visible == nil || visible[i] {
			s.lines[i] = true
		}
	}
}

func (s *selectionState) clear() {
	s.lines = nil
	s.anchor = 0
}

// shiftEvicted shifts every selected index left by n (the number of
// entries the bounded log buffer just dropped from its head). Lines
// that fall off the start are removed silently — once they're gone
// from the buffer the user can no longer see them, so a phantom
// selection would be confusing.
func (s *selectionState) shiftEvicted(n int) {
	if n <= 0 || !s.hasAny() {
		return
	}
	next := make(map[int]bool, len(s.lines))
	for ix := range s.lines {
		shifted := ix - n
		if shifted >= 0 {
			next[shifted] = true
		}
	}
	s.lines = next
	s.anchor -= n
	if s.anchor < 0 {
		// Anchor evicted — pin it to the smallest surviving entry so
		// the next shift-click still has a reference point. If the
		// whole selection is gone, fall back to 0; clickRange treats
		// no-selection as "click line is the anchor" anyway.
		if len(next) == 0 {
			s.anchor = 0
			return
		}
		minIx := -1
		for ix := range next {
			if minIx < 0 || ix < minIx {
				minIx = ix
			}
		}
		s.anchor = minIx
	}
}

// snapshot returns the selected lines from buffer in original buffer
// order (smallest index first). Indices that fall outside the buffer
// are skipped — defensive against any caller that forgot shiftEvicted.
func (s *selectionState) snapshot(buffer []string) []string {
	if !s.hasAny() {
		return nil
	}
	order := make([]int, 0, len(s.lines))
	for ix := range s.lines {
		if ix >= 0 && ix < len(buffer) {
			order = append(order, ix)
		}
	}
	sort.Ints(order)
	out := make([]string, 0, len(order))
	for _, ix := range order {
		out = append(out, buffer[ix])
	}
	return out
}

// copyToClipboard joins the selected lines as plaintext (ANSI
// escapes stripped) and writes them to the system clipboard. Returns
// the underlying clipboard error so the model can surface it on the
// hamr log — the helper binaries (xclip / wl-copy / pbcopy) might
// not be installed on the host.
func (s *selectionState) copyToClipboard(buffer []string) error {
	lines := s.snapshot(buffer)
	if len(lines) == 0 {
		return nil
	}
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(stripANSI(line))
	}
	return clipboard.WriteAll(b.String())
}

// ansiCSI matches a CSI escape sequence: ESC `[`, optional parameter
// bytes (0x30..0x3F), optional intermediate bytes (0x20..0x2F), then
// a final byte (0x40..0x7E). Conservative — anything not in this
// shape is left in place rather than risking damage to the line.
var ansiCSI = regexp.MustCompile("\x1b\\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]")

func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	return ansiCSI.ReplaceAllString(s, "")
}

// SGR pair for plain reverse-video. Direct codes (rather than going
// through lipgloss) because termenv strips styles outside a TTY,
// which would silently disable selection highlighting in tests and
// in any environment where the color profile has been forced down.
const (
	sgrReverse = "\x1b[7m"
	sgrReset   = "\x1b[0m"
)

// renderSelected wraps a chunk in reverse-video. Naive `[7m...[0m`
// breaks on lines that contain embedded resets (rule-prefix coloring,
// `[make:target]` tags, search highlights all emit a `[0m` at the end
// of their styled span) — the embedded `[0m` would turn reverse off
// mid-line. So every embedded reset gets a `[7m` re-emitted right
// after it, restoring the inverted style for the remainder of the
// chunk while leaving any later styled spans free to apply their own
// colors on top.
func renderSelected(chunk string) string {
	if !strings.Contains(chunk, sgrReset) {
		return sgrReverse + chunk + sgrReset
	}
	return sgrReverse + strings.ReplaceAll(chunk, sgrReset, sgrReset+sgrReverse) + sgrReset
}

// renderWithSelection turns a per-line slice into the single string
// the viewport expects. Each entry is hardwrapped at width and, if
// its corresponding bufferIx is in sel.lines, every wrapped chunk is
// wrapped in reverse-video so the whole logical line reads as
// selected — including any continuation rows from the wrap.
//
// bufferIx is the parallel slice of buffer line indices: callers pass
// identity (0..n-1) when the per-line slice is the buffer as-is, and
// the matched-line order when search filtering is on.
func renderWithSelection(perLine []string, bufferIx []int, sel *selectionState, width int) string {
	if width <= 0 || len(perLine) == 0 {
		return strings.Join(perLine, "\n")
	}
	var b strings.Builder
	for i, line := range perLine {
		if i > 0 {
			b.WriteByte('\n')
		}
		chunks := hardwrap(line, width)
		ix := i
		if i < len(bufferIx) {
			ix = bufferIx[i]
		}
		selected := sel.hasAny() && sel.lines[ix]
		for j, c := range chunks {
			if j > 0 {
				b.WriteByte('\n')
			}
			if selected {
				b.WriteString(renderSelected(c))
			} else {
				b.WriteString(c)
			}
		}
	}
	return b.String()
}

// visualRowToLine maps a visual row index (0 == first row of the
// viewport content) to the position in perLine it falls inside.
// Returns -1 when the row is past the end of the rendered content,
// which the caller treats as "click on empty space — ignore".
//
// width must match the viewport width the content was rendered with;
// otherwise the row counts won't line up with what the user saw.
func visualRowToLine(perLine []string, width, row int) int {
	if width <= 0 || row < 0 {
		return -1
	}
	cum := 0
	for i, line := range perLine {
		rows := visualRowCount(line, width)
		if row < cum+rows {
			return i
		}
		cum += rows
	}
	return -1
}

// visualRowCount returns how many visual rows hardwrap would emit
// for line at the given width without actually allocating chunks.
// Mirrors hardwrap's column accounting (CSI escapes don't count) so
// the row math always agrees with what setViewContent painted.
func visualRowCount(line string, width int) int {
	if width <= 0 {
		return 1
	}
	visible := 0
	rows := 1
	i := 0
	for i < len(line) {
		if line[i] == 0x1b {
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++
				for j < len(line) {
					c := line[j]
					j++
					if c >= 0x40 && c <= 0x7e {
						break
					}
				}
				i = j
				continue
			}
			i++
			continue
		}
		_, sz := utf8.DecodeRuneInString(line[i:])
		if visible == width {
			rows++
			visible = 0
		}
		visible++
		i += sz
	}
	return rows
}
