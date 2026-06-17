package i18n

import (
	"bytes"
	"maps"
	"sort"
	"strings"
	"text/template"
)

// message is a single translation entry — either a plain/interpolated string
// or a plural map keyed by CLDR category.
type message struct {
	text   string                                // plain string (may contain {{.Var}})
	plural map[PluralCategory]string             // non-nil for plural messages
	tmpl   *template.Template                    // pre-parsed template (nil if no interpolation)
	ptmpls map[PluralCategory]*template.Template // per-category templates
}

// newMessage creates a message from a plain string, pre-parsing any
// interpolation templates.
func newMessage(text string) (message, error) {
	m := message{text: text}
	if strings.Contains(text, "{{") {
		t, err := template.New("").Parse(text)
		if err != nil {
			return m, err
		}
		m.tmpl = t
	}
	return m, nil
}

// newPluralMessage creates a message from a plural map.
func newPluralMessage(obj map[string]any) (message, error) {
	m := message{
		plural: make(map[PluralCategory]string, len(obj)),
		ptmpls: make(map[PluralCategory]*template.Template, len(obj)),
	}
	for k, v := range obj {
		cat := validPluralCategories[k]
		s := v.(string) //nolint:errcheck // isPluralObject already validated
		m.plural[cat] = s
		if strings.Contains(s, "{{") {
			t, err := template.New("").Parse(s)
			if err != nil {
				return m, err
			}
			m.ptmpls[cat] = t
		}
	}
	return m, nil
}

// isPlural reports whether this is a plural message.
func (m message) isPlural() bool {
	return m.plural != nil
}

// vars returns the sorted interpolation variable names in this message.
func (m message) vars() []string {
	if m.isPlural() {
		seen := map[string]bool{}
		var all []string
		for _, s := range m.plural {
			for _, v := range InterpolationVars(s) {
				if !seen[v] {
					seen[v] = true
					all = append(all, v)
				}
			}
		}
		sort.Strings(all)
		return all
	}
	return InterpolationVars(m.text)
}

// Translator holds the messages for a single locale and resolves translations.
type Translator struct {
	locale     string
	messages   map[string]message
	pluralRule PluralRule
	direction  string
	fallback   *Translator
}

// newTranslator creates a Translator for the given locale.
func newTranslator(locale string, msgs map[string]message, direction string, fallback *Translator) *Translator {
	return &Translator{
		locale:     locale,
		messages:   msgs,
		pluralRule: RuleFor(locale),
		direction:  direction,
		fallback:   fallback,
	}
}

// Locale returns the locale code (e.g. "en").
func (t *Translator) Locale() string { return t.locale }

// Direction returns the text direction ("ltr" or "rtl").
func (t *Translator) Direction() string { return t.direction }

// Has reports whether a key exists in this translator (not checking fallback).
func (t *Translator) Has(key string) bool {
	_, ok := t.messages[key]
	return ok
}

// T translates a key with optional arguments.
//
// Argument patterns:
//   - No args: return plain string.
//   - int arg: use as plural count; remaining args for interpolation data.
//   - map[string]any arg: use as interpolation data.
//
// Falls back to the fallback translator, then returns the key as-is.
func (t *Translator) T(key string, args ...any) string {
	msg, ok := t.messages[key]
	if !ok {
		if t.fallback != nil {
			return t.fallback.T(key, args...)
		}
		return key
	}
	return t.renderMessage(key, msg, args)
}

func (t *Translator) renderMessage(key string, msg message, args []any) string {
	if msg.isPlural() {
		return t.renderPlural(key, msg, args)
	}
	return t.renderPlain(msg, args)
}

// pluralCategoryOrder is the canonical CLDR ordering used to pick a category
// deterministically when neither the count's category nor Other is defined.
// Ranging a map directly would render a different string run-to-run.
var pluralCategoryOrder = []PluralCategory{Zero, One, Two, Few, Many, Other}

func (t *Translator) renderPlain(msg message, args []any) string {
	if msg.tmpl == nil {
		return msg.text
	}
	data := extractData(args)
	var buf bytes.Buffer
	if err := msg.tmpl.Execute(&buf, data); err != nil {
		return msg.text
	}
	return buf.String()
}

func (t *Translator) renderPlural(key string, msg message, args []any) string {
	count := 0
	var remaining []any
	if len(args) > 0 {
		if n, ok := args[0].(int); ok {
			count = n
			remaining = args[1:]
		} else {
			remaining = args
		}
	}

	cat := t.pluralRule(count)
	text, ok := msg.plural[cat]
	if !ok {
		if v, has := msg.plural[Other]; has {
			cat, text, ok = Other, v, true
		}
	}
	if !ok {
		// The locale's plural message defines neither the count's category nor
		// Other. Prefer the fallback locale's (complete) plural for this key —
		// it has a proper category for the count — over picking an arbitrary
		// local category, which would be non-deterministic.
		if t.fallback != nil {
			return t.fallback.T(key, args...)
		}
		// No fallback: choose deterministically by canonical CLDR order.
		for _, c := range pluralCategoryOrder {
			if v, has := msg.plural[c]; has {
				cat, text, ok = c, v, true
				break
			}
		}
		if !ok {
			return key // empty plural map — should not happen
		}
	}

	tmpl := msg.ptmpls[cat]
	if tmpl == nil {
		return text
	}

	data := extractData(remaining)
	// Build a new map so we don't mutate the caller's data.
	merged := make(map[string]any, len(data)+1)
	maps.Copy(merged, data)
	if _, ok := merged["Count"]; !ok {
		merged["Count"] = count
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, merged); err != nil {
		return text
	}
	return buf.String()
}

// extractData pulls a map[string]any from the args list.
func extractData(args []any) map[string]any {
	for _, a := range args {
		if m, ok := a.(map[string]any); ok {
			return m
		}
	}
	return nil
}
