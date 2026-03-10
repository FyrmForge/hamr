package i18n

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/ptr"
)

func TestNewBundle(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:         "testdata",
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	if !b.HasLocale("en") {
		t.Error("should have en")
	}
	if !b.HasLocale("fr") {
		t.Error("should have fr")
	}

	supported := b.SupportedLocales()
	if len(supported) != 2 {
		t.Errorf("supported = %v, want 2 locales", supported)
	}

	if b.DefaultLocale() != "en" {
		t.Errorf("default = %q, want %q", b.DefaultLocale(), "en")
	}
}

func TestBundleTranslator(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:         "testdata",
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	en := b.Translator("en")
	if got := en.T("app.title"); got != "My App" {
		t.Errorf("en app.title = %q, want %q", got, "My App")
	}

	fr := b.Translator("fr")
	if got := fr.T("app.title"); got != "Mon App" {
		t.Errorf("fr app.title = %q, want %q", got, "Mon App")
	}
}

func TestBundleUnknownLocaleFallsBack(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:         "testdata",
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	tr := b.Translator("xx")
	if tr.Locale() != "en" {
		t.Errorf("unknown locale returned %q, want fallback to %q", tr.Locale(), "en")
	}
}

func TestBundleMissingDir(t *testing.T) {
	_, err := NewBundle(BundleConfig{
		LocaleDir:     "nonexistent",
		DefaultLocale: "en",
	})
	if err == nil {
		t.Error("expected error for missing dir")
	}
}

func TestBundleMissingDefaultLocale(t *testing.T) {
	_, err := NewBundle(BundleConfig{
		LocaleDir:     "testdata",
		DefaultLocale: "xx",
	})
	if err == nil {
		t.Error("expected error for missing default locale")
	}
}

func TestBundleUnparseableDefaultLocale(t *testing.T) {
	_, err := NewBundle(BundleConfig{
		LocaleDir:     "testdata/invalid",
		DefaultLocale: "bad",
	})
	if err == nil {
		t.Error("expected error for unparseable default locale JSON")
	}
}

func TestBundleFrenchPlural(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:         "testdata",
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	fr := b.Translator("fr")
	if got := fr.T("home.items", 1); got != "1 objet" {
		t.Errorf("fr items(1) = %q, want %q", got, "1 objet")
	}
	if got := fr.T("home.items", 5); got != "5 objets" {
		t.Errorf("fr items(5) = %q, want %q", got, "5 objets")
	}
}

func TestBundleFallbackToDefaultNilDefaultsToTrue(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:     "testdata",
		DefaultLocale: "en",
		// FallbackToDefault is nil — should default to true.
	})
	if err != nil {
		t.Fatal(err)
	}

	// "only_en" exists only in en.json — fr should fall back.
	fr := b.Translator("fr")
	if got := fr.T("only_en"); got != "English only" {
		t.Errorf("expected fallback value %q, got %q", "English only", got)
	}
}

func TestBundleFallbackToDefaultFalse(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:         "testdata",
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(false),
	})
	if err != nil {
		t.Fatal(err)
	}

	// "only_en" exists in en.json but not fr.json — with no fallback the raw key is returned.
	fr := b.Translator("fr")
	if got := fr.T("only_en"); got != "only_en" {
		t.Errorf("expected raw key %q, got %q — fallback should be disabled", "only_en", got)
	}
}

func TestNewBundleInterpolationMismatch(t *testing.T) {
	dir := t.TempDir()

	en := `{"greeting": "Hello {{.Name}}"}`
	fr := `{"greeting": "Bonjour {{.FirstName}}"}`

	if err := os.WriteFile(filepath.Join(dir, "en.json"), []byte(en), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fr.json"), []byte(fr), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewBundle(BundleConfig{
		LocaleDir:         dir,
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	if err == nil {
		t.Fatal("expected error for interpolation mismatch")
	}
	if !strings.Contains(err.Error(), "interpolation mismatch") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "interpolation mismatch")
	}
}

func TestBundleResolveLocale(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:         "testdata",
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		code    string
		wantLoc string
		wantOK  bool
	}{
		{"fr", "fr", true},
		{"fr-FR", "fr", true},
		{"en", "en", true},
		{"en-US", "en", true},
		{"xx", "", false},
		{"xx-YY", "", false},
	}
	for _, tt := range tests {
		loc, ok := b.ResolveLocale(tt.code)
		if loc != tt.wantLoc || ok != tt.wantOK {
			t.Errorf("ResolveLocale(%q) = (%q, %v), want (%q, %v)", tt.code, loc, ok, tt.wantLoc, tt.wantOK)
		}
	}
}

func TestBundleInterpolation(t *testing.T) {
	b, err := NewBundle(BundleConfig{
		LocaleDir:         "testdata",
		DefaultLocale:     "en",
		FallbackToDefault: ptr.To(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	en := b.Translator("en")
	got := en.T("home.welcome", map[string]any{"Name": "World"})
	if got != "Welcome, World!" {
		t.Errorf("got %q, want %q", got, "Welcome, World!")
	}
}
