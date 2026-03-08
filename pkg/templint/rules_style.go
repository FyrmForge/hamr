package templint

import "regexp"

var (
	inlineStyleRe     = regexp.MustCompile(`style="[^"]*"`)
	inlineStyleTemplRe = regexp.MustCompile(`style=\{`)
	emptyClassRe      = regexp.MustCompile(`class=""`)
	jsHrefRe          = regexp.MustCompile(`href="javascript:`)
)

type inlineStyleRule struct {
	severity Severity
}

func (r *inlineStyleRule) ID() string { return "inline-style" }

func (r *inlineStyleRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for i, line := range lines {
		if loc := inlineStyleRe.FindStringIndex(line); loc != nil {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     i + 1,
				Col:      loc[0] + 1,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "inline style attribute found; use CSS classes instead",
			})
		} else if loc := inlineStyleTemplRe.FindStringIndex(line); loc != nil {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     i + 1,
				Col:      loc[0] + 1,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "inline style attribute found; use CSS classes instead",
			})
		}
	}
	return diags
}

type emptyClassRule struct {
	severity Severity
}

func (r *emptyClassRule) ID() string { return "empty-class" }

func (r *emptyClassRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for i, line := range lines {
		if loc := emptyClassRe.FindStringIndex(line); loc != nil {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     i + 1,
				Col:      loc[0] + 1,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "empty class attribute",
			})
		}
	}
	return diags
}

type jsHrefRule struct {
	severity Severity
}

func (r *jsHrefRule) ID() string { return "js-href" }

func (r *jsHrefRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for i, line := range lines {
		if loc := jsHrefRe.FindStringIndex(line); loc != nil {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     i + 1,
				Col:      loc[0] + 1,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  "javascript: href is unsafe; use event handlers instead",
			})
		}
	}
	return diags
}
