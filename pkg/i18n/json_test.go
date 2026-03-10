package i18n

import "testing"

func TestFlattenMessages(t *testing.T) {
	data := map[string]any{
		"app": map[string]any{
			"title": "My App",
		},
		"home": map[string]any{
			"welcome": "Hello {{.Name}}",
			"items": map[string]any{
				"one":   "1 item",
				"other": "{{.Count}} items",
			},
		},
		"_meta": map[string]any{
			"direction": "ltr",
		},
	}

	msgs, err := flattenMessages(data, "")
	if err != nil {
		t.Fatal(err)
	}

	// _meta should be skipped.
	if _, ok := msgs["_meta"]; ok {
		t.Error("_meta should be skipped")
	}
	if _, ok := msgs["_meta.direction"]; ok {
		t.Error("_meta.direction should be skipped")
	}

	// Flat keys.
	if msg, ok := msgs["app.title"]; !ok {
		t.Error("missing app.title")
	} else if msg.text != "My App" {
		t.Errorf("app.title = %q, want %q", msg.text, "My App")
	}

	// Interpolation.
	if msg, ok := msgs["home.welcome"]; !ok {
		t.Error("missing home.welcome")
	} else if msg.tmpl == nil {
		t.Error("home.welcome should have a template")
	}

	// Plural.
	if msg, ok := msgs["home.items"]; !ok {
		t.Error("missing home.items")
	} else if !msg.isPlural() {
		t.Error("home.items should be plural")
	}
}

func TestIsPluralObject(t *testing.T) {
	tests := []struct {
		name string
		obj  map[string]any
		want bool
	}{
		{"valid", map[string]any{"one": "a", "other": "b"}, true},
		{"all cats", map[string]any{"zero": "z", "one": "o", "two": "t", "few": "f", "many": "m", "other": "x"}, true},
		{"non-plural key", map[string]any{"one": "a", "title": "b"}, false},
		{"non-string value", map[string]any{"one": 1, "other": 2}, false},
		{"empty", map[string]any{}, false},
		{"nested obj", map[string]any{"one": "a", "other": map[string]any{"nested": "x"}}, false},
	}
	for _, tt := range tests {
		if got := IsPluralObject(tt.obj); got != tt.want {
			t.Errorf("IsPluralObject(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestExtractMeta(t *testing.T) {
	data := map[string]any{
		"_meta": map[string]any{
			"direction":   "rtl",
			"displayName": "Arabic",
		},
	}
	meta := extractMeta(data)
	if meta == nil {
		t.Fatal("expected meta")
	}
	if meta.Direction != "rtl" {
		t.Errorf("direction = %q, want %q", meta.Direction, "rtl")
	}
	if meta.DisplayName != "Arabic" {
		t.Errorf("displayName = %q, want %q", meta.DisplayName, "Arabic")
	}
}

func TestExtractMetaMissing(t *testing.T) {
	data := map[string]any{"app": map[string]any{"title": "test"}}
	meta := extractMeta(data)
	if meta != nil {
		t.Error("expected nil meta")
	}
}

func TestValidateLocale(t *testing.T) {
	defaultMsgs := map[string]message{
		"app.title":    {text: "App"},
		"home.welcome": {text: "Hello {{.Name}}"},
	}
	localeMsgs := map[string]message{
		"app.title":    {text: "Mon App"},
		"home.welcome": {text: "Bonjour {{.FirstName}}"},
		"extra.key":    {text: "Extra"},
	}

	errs := validateLocale(defaultMsgs, localeMsgs, "fr")

	// Should have: extra key + interpolation mismatch.
	if len(errs) != 2 {
		t.Fatalf("got %d errors, want 2: %v", len(errs), errs)
	}
}

func TestInterpolationVars(t *testing.T) {
	vars := InterpolationVars("Hello {{.Name}}, you have {{.Count}} items from {{.Name}}")
	if len(vars) != 2 {
		t.Fatalf("got %d vars, want 2", len(vars))
	}
	if vars[0] != "Count" || vars[1] != "Name" {
		t.Errorf("vars = %v, want [Count Name]", vars)
	}
}
