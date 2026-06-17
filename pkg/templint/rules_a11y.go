package templint

import (
	"regexp"
	"strings"
)

var (
	altAttrRe  = regexp.MustCompile(`(?i)(^|[\s])alt\s*=`)
	hrefAttrRe = regexp.MustCompile(`(?i)(^|[\s])href\s*=`)
)

type imgAltRule struct {
	severity Severity
}

func (r *imgAltRule) ID() string { return "img-alt" }

func (r *imgAltRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for _, tag := range collectOpenTags(lines, "img") {
		if !altAttrRe.MatchString(tag.text) {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     tag.line,
				Col:      tag.col,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "<img> tag missing alt attribute",
			})
		}
	}
	return diags
}

type noHrefRule struct {
	severity Severity
}

func (r *noHrefRule) ID() string { return "no-href" }

func (r *noHrefRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for _, tag := range collectOpenTags(lines, "a") {
		if !hrefAttrRe.MatchString(tag.text) {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     tag.line,
				Col:      tag.col,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "<a> tag missing href attribute",
			})
		}
	}
	return diags
}

type openTag struct {
	line int
	col  int
	text string
}

func collectOpenTags(lines []string, tag string) []openTag {
	var tags []openTag

	inTag := false
	startLine := 0
	startCol := 0
	var buf strings.Builder

	for i, line := range lines {
		offset := 0
		baseCol := 0

		if inTag {
			if buf.Len() > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString(line)

			// Find the closing '>' against the full accumulated buffer so quote
			// state carried from earlier lines is honoured.
			full := buf.String()
			if cut := findTagEnd(full); cut >= 0 {
				tags = append(tags, openTag{
					line: startLine,
					col:  startCol,
					text: full[:cut+1],
				})
				inTag = false
				buf.Reset()
				// Map the buffer-relative close back to the current line so the
				// remainder can be scanned for more tags.
				tailStart := max(cut+1-(len(full)-len(line)), 0)
				baseCol += tailStart
				line = line[tailStart:]
				offset = 0
			} else {
				continue
			}
		}

		for {
			pos := findTagStart(line, tag, offset)
			if pos < 0 {
				break
			}

			rest := line[pos:]
			end := findTagEnd(rest)
			if end >= 0 {
				tags = append(tags, openTag{
					line: i + 1,
					col:  baseCol + pos + 1,
					text: rest[:end+1],
				})
				offset = pos + end + 1
				continue
			}

			inTag = true
			startLine = i + 1
			startCol = pos + 1
			buf.Reset()
			buf.WriteString(rest)
			break
		}
	}

	return tags
}

// findTagEnd returns the index of the first '>' that actually closes the tag —
// i.e. the first '>' not inside a single- or double-quoted attribute value.
// Without this a value like title="a > b" or data-x="x>y" would be treated as
// the tag end, hiding every attribute after it (e.g. alt=/href=) from the
// a11y checks and producing false "missing attribute" diagnostics.
func findTagEnd(s string) int {
	var quote byte
	for i := range len(s) {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

func findTagStart(line, tag string, from int) int {
	if from < 0 {
		from = 0
	}
	if tag == "" {
		return findAnyTagStart(line, from)
	}
	needle := "<" + tag
	for from < len(line) {
		idx := strings.Index(line[from:], needle)
		if idx < 0 {
			return -1
		}
		idx += from
		next := idx + len(needle)
		if next >= len(line) || isTagBoundary(line[next]) {
			return idx
		}
		from = idx + 1
	}
	return -1
}

// findAnyTagStart finds the next opening HTML element start (e.g. "<div", "<a")
// by matching "<" followed by an ASCII letter. Closing tags "</foo>", comments
// "<!--", and templ expressions "<{...}>" are skipped automatically because
// their second character is not a letter.
func findAnyTagStart(line string, from int) int {
	for i := from; i < len(line)-1; i++ {
		if line[i] != '<' {
			continue
		}
		c := line[i+1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return i
		}
	}
	return -1
}

func isTagBoundary(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '/', '>':
		return true
	default:
		return false
	}
}
