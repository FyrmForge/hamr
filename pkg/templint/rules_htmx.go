package templint

import (
	"fmt"
	"regexp"
	"sort"
)

var (
	nativeFormAttrRe = regexp.MustCompile(`(?i)(^|[\s])(action|method|formaction)\s*=`)
	// Matches both the canonical hx-* form and the HTML5-compliant data-hx-* form.
	hxAttrRe = regexp.MustCompile(`(?i)(^|[\s])((?:data-)?hx-[a-z][a-z0-9-]*)\s*=`)
)

type noNativeFormActionsRule struct {
	severity Severity
}

func (r *noNativeFormActionsRule) ID() string { return "no-native-form-actions" }

func (r *noNativeFormActionsRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for _, tag := range collectOpenTags(lines, "") {
		for _, attr := range findNativeFormAttrs(tag.text) {
			diags = append(diags, Diagnostic{
				File:     filename,
				Line:     tag.line,
				Col:      tag.col,
				Rule:     r.ID(),
				Severity: r.severity,
				Message:  fmt.Sprintf("native form attribute %q found; use htmx attributes (hx-post, hx-get, ...) instead", attr),
			})
		}
	}
	return diags
}

type htmxConflictRule struct {
	severity Severity
}

func (r *htmxConflictRule) ID() string { return "htmx-conflict" }

func (r *htmxConflictRule) Check(filename string, lines []string) []Diagnostic {
	var diags []Diagnostic
	for _, tag := range collectOpenTags(lines, "") {
		native := findNativeFormAttrs(tag.text)
		if len(native) == 0 {
			continue
		}
		hx := findHxAttrs(tag.text)
		if len(hx) == 0 {
			continue
		}
		diags = append(diags, Diagnostic{
			File:     filename,
			Line:     tag.line,
			Col:      tag.col,
			Rule:     r.ID(),
			Severity: r.severity,
			Message:  fmt.Sprintf("element mixes htmx (%v) and native form attributes (%v); pick one", hx, native),
		})
	}
	return diags
}

// findNativeFormAttrs returns the unique native form attribute names found in
// the given open-tag text, sorted alphabetically.
func findNativeFormAttrs(tagText string) []string {
	return findAttrs(nativeFormAttrRe, tagText)
}

// findHxAttrs returns the unique hx-* (or data-hx-*) attribute names found in
// the given open-tag text, sorted alphabetically.
func findHxAttrs(tagText string) []string {
	return findAttrs(hxAttrRe, tagText)
}

// findAttrs runs re over tagText (re must capture the attribute name as
// submatch index 2) and returns the unique names sorted alphabetically.
func findAttrs(re *regexp.Regexp, tagText string) []string {
	matches := re.FindAllStringSubmatch(tagText, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		seen[m[2]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
