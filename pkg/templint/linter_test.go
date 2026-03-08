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

	linter := New(nil)
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

func TestConfigDisablesRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.templ")
	if err := os.WriteFile(path, []byte("  if ok { <span>yes</span> }"), 0644); err != nil {
		t.Fatal(err)
	}

	f := false
	cfg := &Config{
		Rules: map[string]RuleConfig{
			"inline-if": {Enabled: &f},
		},
	}
	linter := New(cfg)
	diags, err := linter.LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule == "inline-if" {
			t.Error("inline-if should be disabled")
		}
	}
}

func TestConfigOverridesSeverity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.templ")
	if err := os.WriteFile(path, []byte("  <img src=\"x.png\">"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Rules: map[string]RuleConfig{
			"img-alt": {Severity: "error"},
		},
	}
	linter := New(cfg)
	diags, err := linter.LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule == "img-alt" && d.Severity != Error {
			t.Errorf("expected error severity, got %s", d.Severity)
		}
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/.templint.yml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Error("expected nil config for missing file")
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".templint.yml")
	content := "rules:\n  inline-if:\n    enabled: false\n    severity: warning\n"
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
	if cfg.IsEnabled("inline-if") {
		t.Error("inline-if should be disabled")
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
