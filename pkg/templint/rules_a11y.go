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

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		offset := 0
		baseCol := 0

		if inTag {
			if buf.Len() > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString(line)

			if end := strings.Index(line, ">"); end >= 0 {
				tagText := buf.String()
				if cut := strings.Index(tagText, ">"); cut >= 0 {
					tagText = tagText[:cut+1]
				}
				tags = append(tags, openTag{
					line: startLine,
					col:  startCol,
					text: tagText,
				})
				inTag = false
				buf.Reset()
				line = line[end+1:]
				baseCol += end + 1
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
			end := strings.Index(rest, ">")
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

func findTagStart(line, tag string, from int) int {
	if from < 0 {
		from = 0
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

func isTagBoundary(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '/', '>':
		return true
	default:
		return false
	}
}
