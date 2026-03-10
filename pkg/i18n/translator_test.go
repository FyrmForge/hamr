package i18n

import "testing"

func TestTranslatorPlainString(t *testing.T) {
	msgs := map[string]message{
		"app.title": {text: "My App"},
	}
	tr := newTranslator("en", msgs, "ltr", nil)

	if got := tr.T("app.title"); got != "My App" {
		t.Errorf("T(app.title) = %q, want %q", got, "My App")
	}
}

func TestTranslatorInterpolation(t *testing.T) {
	m, err := newMessage("Hello, {{.Name}}!")
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]message{"greeting": m}
	tr := newTranslator("en", msgs, "ltr", nil)

	got := tr.T("greeting", map[string]any{"Name": "Alice"})
	if got != "Hello, Alice!" {
		t.Errorf("got %q, want %q", got, "Hello, Alice!")
	}
}

func TestTranslatorPlural(t *testing.T) {
	pm, err := newPluralMessage(map[string]any{
		"one":   "{{.Count}} item",
		"other": "{{.Count}} items",
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]message{"items": pm}
	tr := newTranslator("en", msgs, "ltr", nil)

	tests := []struct {
		count int
		want  string
	}{
		{0, "0 items"},
		{1, "1 item"},
		{5, "5 items"},
	}
	for _, tt := range tests {
		got := tr.T("items", tt.count)
		if got != tt.want {
			t.Errorf("T(items, %d) = %q, want %q", tt.count, got, tt.want)
		}
	}
}

func TestTranslatorFallback(t *testing.T) {
	enMsgs := map[string]message{
		"app.title": {text: "My App"},
		"only.en":   {text: "English only"},
	}
	frMsgs := map[string]message{
		"app.title": {text: "Mon App"},
	}

	en := newTranslator("en", enMsgs, "ltr", nil)
	fr := newTranslator("fr", frMsgs, "ltr", en)

	if got := fr.T("app.title"); got != "Mon App" {
		t.Errorf("fr app.title = %q, want %q", got, "Mon App")
	}
	if got := fr.T("only.en"); got != "English only" {
		t.Errorf("fr only.en = %q, want %q (should fall back to en)", got, "English only")
	}
}

func TestTranslatorMissingKeyReturnsKey(t *testing.T) {
	tr := newTranslator("en", map[string]message{}, "ltr", nil)
	if got := tr.T("missing.key"); got != "missing.key" {
		t.Errorf("got %q, want %q", got, "missing.key")
	}
}

func TestTranslatorDirection(t *testing.T) {
	tr := newTranslator("ar", map[string]message{}, "rtl", nil)
	if got := tr.Direction(); got != "rtl" {
		t.Errorf("direction = %q, want %q", got, "rtl")
	}
}

func TestTranslatorHas(t *testing.T) {
	msgs := map[string]message{"exists": {text: "yes"}}
	tr := newTranslator("en", msgs, "ltr", nil)
	if !tr.Has("exists") {
		t.Error("Has(exists) should be true")
	}
	if tr.Has("nope") {
		t.Error("Has(nope) should be false")
	}
}

func TestTranslatorPluralFallbackCategoryRendersTemplate(t *testing.T) {
	// Polish: 5 → "many", but we only define "one" and "other".
	// The translator should fall back to "other" AND execute its template.
	pm, err := newPluralMessage(map[string]any{
		"one":   "{{.Count}} element",
		"other": "{{.Count}} elementów",
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]message{"items": pm}
	tr := newTranslator("pl", msgs, "ltr", nil)

	got := tr.T("items", 5)
	if got != "5 elementów" {
		t.Errorf("T(items, 5) = %q, want %q", got, "5 elementów")
	}
}

func TestTranslatorPluralDoesNotMutateCallerData(t *testing.T) {
	pm, err := newPluralMessage(map[string]any{
		"one":   "{{.Count}} item by {{.Author}}",
		"other": "{{.Count}} items by {{.Author}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]message{"items": pm}
	tr := newTranslator("en", msgs, "ltr", nil)

	data := map[string]any{"Author": "Alice"}
	tr.T("items", 5, data)

	// The caller's map must not have been mutated with a "Count" key.
	if _, ok := data["Count"]; ok {
		t.Error("renderPlural mutated caller's data map by injecting Count")
	}
}

func TestTranslatorPluralWithCountAndData(t *testing.T) {
	pm, err := newPluralMessage(map[string]any{
		"one":   "{{.Count}} message for {{.User}}",
		"other": "{{.Count}} messages for {{.User}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]message{"inbox": pm}
	tr := newTranslator("en", msgs, "ltr", nil)

	got := tr.T("inbox", 3, map[string]any{"User": "Bob"})
	if got != "3 messages for Bob" {
		t.Errorf("got %q, want %q", got, "3 messages for Bob")
	}
}
