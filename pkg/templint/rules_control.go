package templint

import "regexp"

var (
	inlineIfRe     = regexp.MustCompile(`^\s*if\s+.+\{.*<.+>`)
	inlineForRe    = regexp.MustCompile(`^\s*for\s+.+\{.*<.+>`)
	inlineSwitchRe = regexp.MustCompile(`^\s*switch\s+.+\{.*<.+>`)
)

type inlineIfRule struct {
	severity Severity
}

func (r *inlineIfRule) ID() string { return "inline-if" }

func (r *inlineIfRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for i, line := range lines {
		if loc := inlineIfRe.FindStringIndex(line); loc != nil {
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
		if loc := inlineForRe.FindStringIndex(line); loc != nil {
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
		if loc := inlineSwitchRe.FindStringIndex(line); loc != nil {
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
