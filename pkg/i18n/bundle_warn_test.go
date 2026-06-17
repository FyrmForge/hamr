package i18n

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLocale(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBundleWarnsOnKeyMismatches(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en.json", `{"a":"A","b":"B"}`)
	// fr is missing "b" and has an extra "c"; interpolation matches (none).
	writeLocale(t, dir, "fr.json", `{"a":"A-fr","c":"C-fr"}`)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	if _, err := NewBundle(BundleConfig{
		LocaleDir:     dir,
		DefaultLocale: "en",
		Logger:        logger,
	}); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "key mismatches") {
		t.Fatalf("expected a key-mismatch warning, got: %q", out)
	}
	if !strings.Contains(out, "b") {
		t.Errorf("expected missing key 'b' in warning, got: %q", out)
	}
	if !strings.Contains(out, "c") {
		t.Errorf("expected extra key 'c' in warning, got: %q", out)
	}
}

func TestBundleInterpolationMismatchIsHardError(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en.json", `{"greet":"Hi {{.Name}}"}`)
	writeLocale(t, dir, "fr.json", `{"greet":"Salut {{.Nom}}"}`) // .Nom != .Name

	_, err := NewBundle(BundleConfig{LocaleDir: dir, DefaultLocale: "en"})
	if err == nil {
		t.Fatal("expected a hard error on interpolation mismatch")
	}
	if !strings.Contains(err.Error(), "interpolation mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}
