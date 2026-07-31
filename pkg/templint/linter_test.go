package templint

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Control flow rules ---

func TestInlineIfDetected(t *testing.T) {
	lines := []string{
		`templ hello() {`,
		`  if user != nil { <span>{ user.Name }</span> }`,
		`}`,
	}
	rule := &inlineIfRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 2, "inline-if", Error)
}

func TestInlineIfMultilineOK(t *testing.T) {
	lines := []string{
		`  if user != nil {`,
		`    <span>{ user.Name }</span>`,
		`  }`,
	}
	rule := &inlineIfRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestInlineForDetected(t *testing.T) {
	lines := []string{
		`  for _, item := range items { <li>{ item }</li> }`,
	}
	rule := &inlineForRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "inline-for", Error)
}

func TestInlineForMultilineOK(t *testing.T) {
	lines := []string{
		`  for _, item := range items {`,
		`    <li>{ item }</li>`,
		`  }`,
	}
	rule := &inlineForRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestInlineSwitchDetected(t *testing.T) {
	lines := []string{
		`  switch status { case "active": <span class="green">Active</span> }`,
	}
	rule := &inlineSwitchRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "inline-switch", Error)
}

func TestInlineSwitchMultilineOK(t *testing.T) {
	lines := []string{
		`  switch status {`,
		`    case "active":`,
		`      <span class="green">Active</span>`,
		`  }`,
	}
	rule := &inlineSwitchRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

// --- Accessibility rules ---

func TestImgAltMissing(t *testing.T) {
	lines := []string{
		`  <img src="photo.jpg">`,
	}
	rule := &imgAltRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "img-alt", Warning)
}

func TestImgAltPresent(t *testing.T) {
	lines := []string{
		`  <img src="photo.jpg" alt="A photo">`,
	}
	rule := &imgAltRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestImgAltDataAltStillMissing(t *testing.T) {
	lines := []string{
		`  <img src="photo.jpg" data-alt="A photo">`,
	}
	rule := &imgAltRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
}

func TestImgAltMultilineMissing(t *testing.T) {
	lines := []string{
		`  <img`,
		`    src="photo.jpg"`,
		`  >`,
	}
	rule := &imgAltRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "img-alt", Warning)
}

func TestImgAltMultilinePresent(t *testing.T) {
	lines := []string{
		`  <img`,
		`    src="photo.jpg"`,
		`    alt="A photo"`,
		`  >`,
	}
	rule := &imgAltRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestNoHrefMissing(t *testing.T) {
	lines := []string{
		`  <a class="link">Click</a>`,
	}
	rule := &noHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "no-href", Warning)
}

func TestNoHrefPresent(t *testing.T) {
	lines := []string{
		`  <a href="/page" class="link">Click</a>`,
	}
	rule := &noHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestNoHrefDataHrefStillMissing(t *testing.T) {
	lines := []string{
		`  <a data-href="/page" class="link">Click</a>`,
	}
	rule := &noHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
}

func TestNoHrefMultipleTagsSameLine(t *testing.T) {
	lines := []string{
		`  <a href="/ok">OK</a><a class="link">Missing</a>`,
	}
	rule := &noHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "no-href", Warning)
}

func TestImgAltMultipleTagsSameLine(t *testing.T) {
	lines := []string{
		`  <img src="ok.jpg" alt="ok"><img src="bad.jpg">`,
	}
	rule := &imgAltRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "img-alt", Warning)
}

func TestNoHrefMultilineMissing(t *testing.T) {
	lines := []string{
		`  <a`,
		`    class="btn"`,
		`  >Click</a>`,
	}
	rule := &noHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "no-href", Warning)
}

func TestNoHrefBareTag(t *testing.T) {
	lines := []string{
		`  <a>Click</a>`,
	}
	rule := &noHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
}

// --- Style rules ---

func TestInlineStyleDetected(t *testing.T) {
	lines := []string{
		`  <div style="color: red;">Hello</div>`,
	}
	rule := &inlineStyleRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "inline-style", Warning)
}

func TestInlineStyleTemplExpr(t *testing.T) {
	lines := []string{
		`  <div style={ templ.SafeCSS("color: red") }>Hello</div>`,
	}
	rule := &inlineStyleRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
}

func TestInlineStyleClean(t *testing.T) {
	lines := []string{
		`  <div class="text-red">Hello</div>`,
	}
	rule := &inlineStyleRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestEmptyClassDetected(t *testing.T) {
	lines := []string{
		`  <div class="">Hello</div>`,
	}
	rule := &emptyClassRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "empty-class", Warning)
}

func TestEmptyClassNonEmpty(t *testing.T) {
	lines := []string{
		`  <div class="container">Hello</div>`,
	}
	rule := &emptyClassRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestJsHrefDetected(t *testing.T) {
	lines := []string{
		`  <a href="javascript:void(0)">Click</a>`,
	}
	rule := &jsHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "js-href", Warning)
}

func TestJsHrefNormal(t *testing.T) {
	lines := []string{
		`  <a href="/page">Click</a>`,
	}
	rule := &jsHrefRule{severity: Warning}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(diags))
	}
}

// --- htmx / native form rules ---

func TestNoNativeFormActionsDetected(t *testing.T) {
	lines := []string{
		`  <form action="/submit" method="post">`,
	}
	rule := &noNativeFormActionsRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics (action + method), got %d: %+v", len(diags), diags)
	}
}

func TestNoNativeFormActionsFormactionOnButton(t *testing.T) {
	lines := []string{
		`  <button formaction="/x" type="submit">Go</button>`,
	}
	rule := &noNativeFormActionsRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for formaction, got %d", len(diags))
	}
	assertDiag(t, diags[0], "test.templ", 1, "no-native-form-actions", Error)
}

func TestNoNativeFormActionsIgnoresDataAttrs(t *testing.T) {
	lines := []string{
		`  <div data-action="ignore" data-method="ignore">x</div>`,
	}
	rule := &noNativeFormActionsRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for data-* attrs, got %d: %+v", len(diags), diags)
	}
}

func TestNoNativeFormActionsClean(t *testing.T) {
	lines := []string{
		`  <form hx-post="/submit">`,
	}
	rule := &noNativeFormActionsRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d: %+v", len(diags), diags)
	}
}

// hx-action="..." (the literal attribute name) must not trigger the native
// rule: the attribute boundary regex requires whitespace or line start before
// "action=", not a "-".
func TestNoNativeFormActionsIgnoresHxAction(t *testing.T) {
	lines := []string{
		`  <button hx-action="x">Click</button>`,
	}
	rule := &noNativeFormActionsRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for hx-action, got %d: %+v", len(diags), diags)
	}
}

func TestNoNativeFormActionsMultiline(t *testing.T) {
	lines := []string{
		`  <form`,
		`    action="/submit"`,
		`    method="post"`,
		`  >`,
	}
	rule := &noNativeFormActionsRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics across multi-line tag, got %d", len(diags))
	}
}

func TestHtmxConflictDetected(t *testing.T) {
	lines := []string{
		`  <form hx-post="/save" action="/submit" method="post">`,
	}
	rule := &htmxConflictRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	assertDiag(t, diags[0], "test.templ", 1, "htmx-conflict", Error)
}

func TestHtmxConflictNoHtmxNoFlag(t *testing.T) {
	lines := []string{
		`  <form action="/submit" method="post">`,
	}
	rule := &htmxConflictRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics (no htmx attr present), got %d", len(diags))
	}
}

func TestHtmxConflictNoNativeNoFlag(t *testing.T) {
	lines := []string{
		`  <form hx-post="/save">`,
	}
	rule := &htmxConflictRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics (no native form attr), got %d", len(diags))
	}
}

func TestHtmxConflictMultiline(t *testing.T) {
	lines := []string{
		`  <form`,
		`    hx-post="/save"`,
		`    action="/legacy"`,
		`  >`,
	}
	rule := &htmxConflictRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for multi-line conflict, got %d: %+v", len(diags), diags)
	}
}

func TestHtmxConflictDataHxPrefix(t *testing.T) {
	lines := []string{
		`  <form data-hx-post="/save" action="/legacy">`,
	}
	rule := &htmxConflictRule{severity: Error}
	diags := rule.Check("test.templ", lines)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for data-hx-* conflict, got %d: %+v", len(diags), diags)
	}
}

// --- Linter engine ---

func TestLintDir(t *testing.T) {
	dir := t.TempDir()
	content := "templ page() {\n  if ok { <span>yes</span> }\n  <img src=\"x.png\">\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "test.templ"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	// Non-templ file should be skipped.
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Rules: map[string]Severity{
		"inline-if": Error,
		"img-alt":   Warning,
	}}
	linter := New(cfg)
	diags, err := linter.LintDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Should find at least: inline-if + img-alt
	if len(diags) < 2 {
		t.Fatalf("expected at least 2 diagnostics, got %d", len(diags))
	}

	// Verify sorted by line.
	for i := 1; i < len(diags); i++ {
		if diags[i].File == diags[i-1].File && diags[i].Line < diags[i-1].Line {
			t.Errorf("diagnostics not sorted: line %d before %d", diags[i-1].Line, diags[i].Line)
		}
	}
}

func TestLintFileNotFound(t *testing.T) {
	linter := New(nil)
	_, err := linter.LintFile("/nonexistent/file.templ")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// --- Config ---

func TestNewNilDisablesEverything(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.templ")
	if err := os.WriteFile(path, []byte("  if ok { <span>yes</span> }"), 0644); err != nil {
		t.Fatal(err)
	}
	linter := New(nil)
	diags, err := linter.LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics from nil config, got %d", len(diags))
	}
}

func TestConfigEnablesOnlyListedRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.templ")
	content := "  if ok { <span>yes</span> }\n  <img src=\"x.png\">\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Rules: map[string]Severity{"img-alt": Warning}}
	linter := New(cfg)
	diags, err := linter.LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule != "img-alt" {
			t.Errorf("unexpected rule %q reported when only img-alt was enabled", d.Rule)
		}
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 img-alt diagnostic, got %d", len(diags))
	}
}

func TestConfigSeverityIsApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.templ")
	if err := os.WriteFile(path, []byte("  <img src=\"x.png\">"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Rules: map[string]Severity{"img-alt": Error}}
	linter := New(cfg)
	diags, err := linter.LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 || diags[0].Severity != Error {
		t.Fatalf("expected one error-severity diagnostic, got %+v", diags)
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/hamr.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Error("expected nil config for missing file")
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hamr.toml")
	content := "[lint.templ]\ninline-if = \"error\"\nimg-alt = \"warning\"\ninline-for = \"off\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if sev, ok := cfg.Rules["inline-if"]; !ok || sev != Error {
		t.Errorf("inline-if: want Error, got ok=%v sev=%v", ok, sev)
	}
	if sev, ok := cfg.Rules["img-alt"]; !ok || sev != Warning {
		t.Errorf("img-alt: want Warning, got ok=%v sev=%v", ok, sev)
	}
	if _, ok := cfg.Rules["inline-for"]; ok {
		t.Error(`inline-for = "off" should be absent from cfg.Rules`)
	}
}

func TestLoadConfigUnknownRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hamr.toml")
	content := "[lint.templ]\nbogus-rule = \"error\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for unknown rule ID")
	}
}

func TestLoadConfigInvalidSeverity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hamr.toml")
	content := "[lint.templ]\ninline-if = \"loud\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

// --- Registry ---

func TestAllRuleIDsCoversEveryRule(t *testing.T) {
	ids := AllRuleIDs()
	expected := []string{
		"empty-class", "htmx-conflict", "img-alt", "inline-for", "inline-if",
		"inline-style", "inline-switch", "js-href", "no-href", "no-native-form-actions",
	}
	if len(ids) != len(expected) {
		t.Fatalf("AllRuleIDs returned %d entries, expected %d: %v", len(ids), len(expected), ids)
	}
	for i, want := range expected {
		if ids[i] != want {
			t.Errorf("AllRuleIDs()[%d] = %q, want %q (full: %v)", i, ids[i], want, ids)
		}
	}
}

func TestDefaultSeverityKnownAndUnknown(t *testing.T) {
	cases := map[string]Severity{
		"inline-if":              Error,
		"no-native-form-actions": Error,
		"htmx-conflict":          Error,
		"img-alt":                Warning,
		"js-href":                Warning,
		"this-rule-does-not-exist": Warning, // fallback
	}
	for id, want := range cases {
		if got := DefaultSeverity(id); got != want {
			t.Errorf("DefaultSeverity(%q) = %s, want %s", id, got, want)
		}
	}
}

// --- Helpers ---

func TestFilterBySeverity(t *testing.T) {
	diags := []Diagnostic{
		{Rule: "a", Severity: Warning},
		{Rule: "b", Severity: Error},
		{Rule: "c", Severity: Warning},
	}
	filtered := FilterBySeverity(diags, Error)
	if len(filtered) != 1 {
		t.Fatalf("expected 1, got %d", len(filtered))
	}
	if filtered[0].Rule != "b" {
		t.Errorf("expected rule b, got %s", filtered[0].Rule)
	}
}

func TestHasErrors(t *testing.T) {
	diags := []Diagnostic{
		{Severity: Warning},
		{Severity: Warning},
	}
	if HasErrors(diags) {
		t.Error("expected no errors")
	}
	diags = append(diags, Diagnostic{Severity: Error})
	if !HasErrors(diags) {
		t.Error("expected errors")
	}
}

func TestDiagnosticString(t *testing.T) {
	d := Diagnostic{
		File:     "test.templ",
		Line:     12,
		Col:      3,
		Rule:     "inline-if",
		Severity: Error,
		Message:  "inline if with HTML body is silently dropped by templ",
	}
	expected := "test.templ:12:3: error[inline-if] inline if with HTML body is silently dropped by templ"
	if got := d.String(); got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

// --- Inline suppression (templint:ignore) ---

// lintSource writes content to a temp .templ file, lints it with the given
// rules enabled, and returns the diagnostics.
func lintSource(t *testing.T, content string, ruleIDs ...string) []Diagnostic {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.templ")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Rules: make(map[string]Severity, len(ruleIDs))}
	for _, id := range ruleIDs {
		cfg.Rules[id] = DefaultSeverity(id)
	}
	diags, err := New(cfg).LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return diags
}

func TestSuppressSingleRule(t *testing.T) {
	src := "// templint:ignore no-native-form-actions -- manifest flow needs a browser POST\n" +
		"<form method=\"post\">\n"
	if diags := lintSource(t, src, "no-native-form-actions"); len(diags) != 0 {
		t.Fatalf("expected directive to suppress the diagnostic, got %+v", diags)
	}
}

func TestSuppressMultipleRules(t *testing.T) {
	src := "// templint:ignore img-alt, empty-class\n" +
		"<img src=\"x.png\" class=\"\">\n"
	if diags := lintSource(t, src, "img-alt", "empty-class"); len(diags) != 0 {
		t.Fatalf("expected both rules suppressed, got %+v", diags)
	}
}

func TestSuppressBareFormSuppressesAll(t *testing.T) {
	src := "// templint:ignore\n" +
		"<img src=\"x.png\" class=\"\">\n"
	if diags := lintSource(t, src, "img-alt", "empty-class"); len(diags) != 0 {
		t.Fatalf("bare directive must suppress every rule, got %+v", diags)
	}
}

func TestSuppressSameLine(t *testing.T) {
	src := "<img src=\"x.png\"> // templint:ignore img-alt\n"
	if diags := lintSource(t, src, "img-alt"); len(diags) != 0 {
		t.Fatalf("trailing directive must suppress its own line, got %+v", diags)
	}
}

// A trailing directive targets only its own line — it must not leak onto the
// line below it.
func TestSuppressSameLineDoesNotLeakToNextLine(t *testing.T) {
	src := "<img src=\"a.png\" alt=\"a\"> // templint:ignore img-alt\n" +
		"<img src=\"b.png\">\n"
	diags := lintSource(t, src, "img-alt")
	if len(diags) != 2 {
		t.Fatalf("expected the line-2 diag plus unused-suppression, got %+v", diags)
	}
	assertDiag(t, diags[0], diags[0].File, 1, "unused-suppression", Warning)
	assertDiag(t, diags[1], diags[1].File, 2, "img-alt", Warning)
}

func TestSuppressUnknownRuleID(t *testing.T) {
	src := "// templint:ignore img-altt\n" +
		"<img src=\"x.png\">\n"
	diags := lintSource(t, src, "img-alt")
	if len(diags) != 2 {
		t.Fatalf("expected the unsuppressed diag plus unknown-rule, got %+v", diags)
	}
	assertDiag(t, diags[0], diags[0].File, 1, "unknown-rule", Error)
	assertDiag(t, diags[1], diags[1].File, 2, "img-alt", Warning)
}

func TestSuppressUnusedDirectiveReported(t *testing.T) {
	src := "// templint:ignore img-alt\n" +
		"<img src=\"x.png\" alt=\"fine\">\n"
	diags := lintSource(t, src, "img-alt")
	if len(diags) != 1 {
		t.Fatalf("expected one unused-suppression, got %+v", diags)
	}
	assertDiag(t, diags[0], diags[0].File, 1, "unused-suppression", Warning)
}

// The documented multi-line contract: rules anchor to the line a tag opens on,
// so the directive belongs above the `<form`, not above the attribute.
func TestSuppressMultilineTag(t *testing.T) {
	src := "// templint:ignore no-native-form-actions -- manifest flow needs a browser POST\n" +
		"<form\n" +
		"  method=\"post\"\n" +
		"  action={ templ.SafeURL(action) }>\n"
	if diags := lintSource(t, src, "no-native-form-actions"); len(diags) != 0 {
		t.Fatalf("directive above the opening <form must cover a multi-line tag, got %+v", diags)
	}
}

// Only the directly adjacent line is covered — no cascading lookback.
func TestSuppressNotAdjacentDoesNotSuppress(t *testing.T) {
	src := "// templint:ignore img-alt\n" +
		"<div>\n" +
		"<img src=\"x.png\">\n"
	diags := lintSource(t, src, "img-alt")
	if len(diags) != 2 {
		t.Fatalf("expected the unsuppressed diag plus unused-suppression, got %+v", diags)
	}
	assertDiag(t, diags[0], diags[0].File, 1, "unused-suppression", Warning)
	assertDiag(t, diags[1], diags[1].File, 3, "img-alt", Warning)
}

// A directive naming a known-but-disabled rule is neither unknown nor unused —
// switching a rule "off" must not turn every existing suppression into noise.
func TestSuppressDisabledRuleIsSilent(t *testing.T) {
	src := "// templint:ignore inline-style\n" +
		"<img src=\"x.png\" alt=\"fine\">\n"
	if diags := lintSource(t, src, "img-alt"); len(diags) != 0 {
		t.Fatalf("directive for a disabled rule must report nothing, got %+v", diags)
	}
}

func assertDiag(t *testing.T, d Diagnostic, file string, line int, rule string, sev Severity) {
	t.Helper()
	if d.File != file {
		t.Errorf("file: got %s, want %s", d.File, file)
	}
	if d.Line != line {
		t.Errorf("line: got %d, want %d", d.Line, line)
	}
	if d.Rule != rule {
		t.Errorf("rule: got %s, want %s", d.Rule, rule)
	}
	if d.Severity != sev {
		t.Errorf("severity: got %s, want %s", d.Severity, sev)
	}
}

// TestInlineIf_NoFalsePositiveOnStringLiteral guards the control-flow fix: an
// HTML-looking token inside a Go string literal must not be flagged as inline
// markup by these Error-severity, CI-gating rules.
func TestInlineIf_NoFalsePositiveOnStringLiteral(t *testing.T) {
	lines := []string{
		`  if ok { fmt.Println("a<b>c") }`, // <b> is inside a Go string, not markup
	}
	rule := &inlineIfRule{severity: Error}
	if diags := rule.Check("test.templ", lines); len(diags) != 0 {
		t.Fatalf("string-literal <b> must not be flagged as inline HTML, got %d diags", len(diags))
	}
}

// TestImgAlt_QuotedGtInAttributeNotTagEnd guards the a11y fix: a '>' inside a
// quoted attribute value must not be treated as the tag end, which would hide
// the alt= attribute that follows and produce a false "missing alt".
func TestImgAlt_QuotedGtInAttributeNotTagEnd(t *testing.T) {
	lines := []string{
		`  <img title="w > h" alt="diagram">`,
	}
	rule := &imgAltRule{severity: Warning}
	if diags := rule.Check("test.templ", lines); len(diags) != 0 {
		t.Fatalf("alt after a quoted '>' must be seen; got %d false diags", len(diags))
	}
}
