package tui

import "strings"

// searchStage tracks where the user is in the / flow.
type searchStage int

const (
	searchClosed    searchStage = iota
	searchPrompting             // typing the query — hint bar shows "/<query>_"
	searchActive                // committed; matches highlighted, n/N navigates
)

// searchMatch points at one substring occurrence inside the current
// log buffer. Offsets are byte offsets into the line — fine for ASCII
// log content; for multi-byte UTF-8 the highlight still falls on a
// valid boundary because strings.Index honours runes.
type searchMatch struct {
	line  int
	start int
	end   int
}

// searchState holds one tab's search context. Pure: open/commit/etc.
// take the buffer they should match against and never touch I/O. The
// model wires viewport scrolling and rendering on top.
type searchState struct {
	stage     searchStage
	query     string
	matches   []searchMatch
	cursor    int  // index into matches; only meaningful when active
	filtering bool // when true, viewport hides non-matching lines
}

// active reports whether the modal/prompt is currently visible.
func (s *searchState) active() bool { return s.stage != searchClosed }

// prompting reports whether the user is mid-query, before commit.
func (s *searchState) prompting() bool { return s.stage == searchPrompting }

// open starts a fresh prompt. Any prior query is discarded — pressing
// `/` while a search is already active means "search again". Filter
// mode resets so each new search starts in highlight-only view.
func (s *searchState) open() {
	s.stage = searchPrompting
	s.query = ""
	s.matches = nil
	s.cursor = 0
	s.filtering = false
}

// appendRune adds a rune to the in-flight query.
func (s *searchState) appendRune(r rune) {
	if s.stage != searchPrompting {
		return
	}
	s.query += string(r)
}

// backspace removes the last rune from the query (no-op on empty).
func (s *searchState) backspace() {
	if s.stage != searchPrompting || s.query == "" {
		return
	}
	r := []rune(s.query)
	s.query = string(r[:len(r)-1])
}

// cancel closes the prompt or active search without leaving traces.
func (s *searchState) cancel() {
	s.stage = searchClosed
	s.query = ""
	s.matches = nil
	s.cursor = 0
	s.filtering = false
}

// toggleFilter flips the filter-only view. Only meaningful in active
// stage — the model gates the binding so this is never called while
// the user is still typing the query.
func (s *searchState) toggleFilter() {
	s.filtering = !s.filtering
}

// matchedLineOrder returns the unique line indices that contain at
// least one match, in the order they appear (which is also sort
// order, since findMatches scans top-down). The filtered renderer
// emits one rendered line per entry.
func (s *searchState) matchedLineOrder() []int {
	if len(s.matches) == 0 {
		return nil
	}
	out := make([]int, 0, len(s.matches))
	seen := make(map[int]bool, len(s.matches))
	for _, m := range s.matches {
		if seen[m.line] {
			continue
		}
		seen[m.line] = true
		out = append(out, m.line)
	}
	return out
}

// filteredCursorLine returns the cursor's row index inside the
// filtered view (i.e. position within matchedLineOrder). The model
// uses this to scroll the viewport when filtering is on.
func (s *searchState) filteredCursorLine() int {
	if !s.filtering || len(s.matches) == 0 {
		return 0
	}
	cur := s.currentMatch()
	idx := 0
	seen := make(map[int]bool, len(s.matches))
	for _, m := range s.matches {
		if seen[m.line] {
			continue
		}
		seen[m.line] = true
		if m.line == cur.line {
			return idx
		}
		idx++
	}
	return 0
}

// commit transitions a prompted search into the navigable "active"
// stage. With live incremental search, matches and cursor are already
// kept up-to-date on every keystroke — commit is just the state flip
// so n/N start navigating instead of being treated as query input.
// A buffer is still accepted so callers can defensively re-scan if
// they have any reason to suspect drift; passing nil skips that.
func (s *searchState) commit(buffer []string) {
	if s.stage != searchPrompting {
		return
	}
	if s.query == "" {
		s.cancel()
		return
	}
	s.stage = searchActive
	if buffer != nil {
		s.recompute(buffer, 0)
	}
}

// recompute refreshes the match list against the current buffer.
// Works in both prompting and active stages: while prompting the
// cursor always resets to 0 (incremental search shows the first hit);
// while active it sticks to the same prior match where possible so
// new log lines arriving don't yank the user mid-navigation.
// evicted is the number of lines the bounded buffer trimmed from its head
// since the last recompute. Match line indices shift down by that amount, so
// the prior anchor is realigned before we look for it — otherwise re-anchoring
// by (line, start) fails on every append at capacity and the cursor snaps back
// to match 0.
func (s *searchState) recompute(buffer []string, evicted int) {
	if s.stage == searchClosed {
		return
	}
	prev := s.currentMatch()
	prev.line -= evicted
	s.matches = findMatches(buffer, s.query)
	if len(s.matches) == 0 {
		s.cursor = 0
		return
	}
	if s.stage == searchPrompting {
		s.cursor = 0
		return
	}
	s.cursor = max(indexOfMatch(s.matches, prev), 0)
}

// next advances the cursor to the next match, wrapping at the end.
func (s *searchState) next() {
	if s.stage != searchActive || len(s.matches) == 0 {
		return
	}
	s.cursor = (s.cursor + 1) % len(s.matches)
}

// prev moves the cursor to the previous match, wrapping at the start.
func (s *searchState) prev() {
	if s.stage != searchActive || len(s.matches) == 0 {
		return
	}
	s.cursor = (s.cursor - 1 + len(s.matches)) % len(s.matches)
}

// currentMatch returns the match at the cursor, or zero value if none.
// Available in both prompting (live search) and active stages so the
// model can scroll to the first hit as the user types.
func (s *searchState) currentMatch() searchMatch {
	if s.stage == searchClosed || len(s.matches) == 0 {
		return searchMatch{}
	}
	if s.cursor < 0 || s.cursor >= len(s.matches) {
		return searchMatch{}
	}
	return s.matches[s.cursor]
}

// findMatches walks the buffer line-by-line and records every
// occurrence of needle (case-insensitive, plain substring — regex is
// out of scope for v1). Lines are scanned with strings.Index on the
// lower-cased copy; offsets returned are into the ORIGINAL line so
// highlight rendering can apply ANSI without re-encoding.
func findMatches(buffer []string, needle string) []searchMatch {
	if needle == "" {
		return nil
	}
	lneedle := strings.ToLower(needle)
	nlen := len(lneedle)
	var out []searchMatch
	for i, line := range buffer {
		ll, origAt := lowerWithOffsets(line)
		off := 0
		for {
			idx := strings.Index(ll[off:], lneedle)
			if idx < 0 {
				break
			}
			start := off + idx
			end := start + nlen
			// origAt translates lower-copy byte offsets back to the original
			// line so highlight rendering slices the right bytes.
			out = append(out, searchMatch{line: i, start: origAt[start], end: origAt[end]})
			off = end
			if off > len(ll) {
				break
			}
		}
	}
	return out
}

// lowerWithOffsets returns the lower-cased form of s plus a table mapping each
// byte offset in the lower-cased string back to the byte offset of the rune it
// came from in the ORIGINAL s. Some runes change byte length when lower-cased
// (İ 2→1, ẞ 3→2, Ⱥ 2→3), so an offset computed against the lower-cased copy
// must be translated before slicing the original. origAt has len(lower)+1
// entries; the final entry is len(s) so an exclusive end offset maps cleanly.
func lowerWithOffsets(s string) (string, []int) {
	var b strings.Builder
	b.Grow(len(s))
	origAt := make([]int, 0, len(s)+1)
	for i, r := range s {
		lr := strings.ToLower(string(r))
		for range len(lr) { // one entry per byte of the lower-cased rune
			origAt = append(origAt, i)
		}
		b.WriteString(lr)
	}
	origAt = append(origAt, len(s))
	return b.String(), origAt
}

// indexOfMatch returns the position of m in matches, or -1 if absent.
// "Same match" is identity by (line, start) — preserved across recompute
// for unmodified prior occurrences.
func indexOfMatch(matches []searchMatch, m searchMatch) int {
	for i, c := range matches {
		if c.line == m.line && c.start == m.start {
			return i
		}
	}
	return -1
}
