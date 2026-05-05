package tui

import (
	"strings"
	"unicode/utf8"
)

// hardwrap splits s into chunks at most width visible columns wide,
// passing CSI escape sequences (ESC [ ... <terminator 0x40-0x7E>)
// through without counting them toward width. Each non-final chunk
// is terminated with a reset (\x1b[0m) so styles applied mid-line
// don't bleed past a wrap point. Each rune is counted as one column;
// wide glyphs (CJK, emoji) may shift the wrap by a column, acceptable
// for log output.
func hardwrap(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	var out []string
	var b strings.Builder
	visible := 0
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) {
					c := s[j]
					j++
					if c >= 0x40 && c <= 0x7e {
						break
					}
				}
				b.WriteString(s[i:j])
				i = j
				continue
			}
			b.WriteByte(s[i])
			i++
			continue
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if visible == width {
			out = append(out, b.String()+"\x1b[0m")
			b.Reset()
			visible = 0
		}
		b.WriteRune(r)
		visible += 1
		i += sz
	}
	out = append(out, b.String())
	return out
}

// wrapForView splits content on existing newlines, hardwraps each line
// at width visible columns, and rejoins. Width <= 0 short-circuits to
// the input unchanged so callers don't have to gate on viewport readiness.
func wrapForView(content string, width int) string {
	if width <= 0 || content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		chunks := hardwrap(line, width)
		for j, c := range chunks {
			if j > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(c)
		}
	}
	return out.String()
}
