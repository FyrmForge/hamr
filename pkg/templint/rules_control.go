package templint

import "regexp"

// The `<[a-zA-Z/]` requires the angle bracket to look like an HTML tag start
// (an element or a closing tag) rather than a Go comparison (`a < b`), channel
// op (`<-ch`), or `<=` — which the old `.*<.+>` matched and falsely flagged.
// String/char literal contents are blanked (blankGoStringContents) before
// matching so an HTML-looking token inside a Go string (`"<b>"`) doesn't trip
// these Error-severity, CI-gating rules either.
var (
	inlineIfRe     = regexp.MustCompile(`^\s*if\s+.+\{.*<[a-zA-Z/].*>`)
	inlineForRe    = regexp.MustCompile(`^\s*for\s+.+\{.*<[a-zA-Z/].*>`)
	inlineSwitchRe = regexp.MustCompile(`^\s*switch\s+.+\{.*<[a-zA-Z/].*>`)
)

// blankGoStringContents replaces the contents of Go string, char, and raw
// string literals with spaces, keeping the delimiters and the overall length so
// reported column numbers stay accurate. This lets the regexes above ignore
// angle brackets that live inside string literals.
func blankGoStringContents(line string) string {
	b := []byte(line)
	var quote byte
	escaped := false
	for i := range len(b) {
		c := b[i]
		if quote != 0 {
			if escaped {
				escaped = false
				b[i] = ' '
				continue
			}
			if c == '\\' && quote != '`' {
				escaped = true
				b[i] = ' '
				continue
			}
			if c == quote {
				quote = 0 // keep the closing delimiter
				continue
			}
			b[i] = ' '
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
		}
	}
	return string(b)
}

type inlineIfRule struct {
	severity Severity
}

func (r *inlineIfRule) ID() string { return "inline-if" }

func (r *inlineIfRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for i, line := range lines {
		if loc := inlineIfRe.FindStringIndex(blankGoStringContents(line)); loc != nil {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     i + 1,
				Col:      loc[0] + 1,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "inline if with HTML body is silently dropped by templ",
			})
		}
	}
	return diags
}

type inlineForRule struct {
	severity Severity
}

func (r *inlineForRule) ID() string { return "inline-for" }

func (r *inlineForRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for i, line := range lines {
		if loc := inlineForRe.FindStringIndex(blankGoStringContents(line)); loc != nil {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     i + 1,
				Col:      loc[0] + 1,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "inline for with HTML body is silently dropped by templ",
			})
		}
	}
	return diags
}

type inlineSwitchRule struct {
	severity Severity
}

func (r *inlineSwitchRule) ID() string { return "inline-switch" }

func (r *inlineSwitchRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for i, line := range lines {
		if loc := inlineSwitchRe.FindStringIndex(blankGoStringContents(line)); loc != nil {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     i + 1,
				Col:      loc[0] + 1,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "inline switch with HTML body is silently dropped by templ",
			})
		}
	}
	return diags
}
