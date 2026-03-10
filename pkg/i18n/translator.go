package i18n

import (
	"bytes"
	"sort"
	"strings"
	"text/template"
)

// message is a single translation entry — either a plain/interpolated string
// or a plural map keyed by CLDR category.
type message struct {
	text   string                       // plain string (may contain {{.Var}})
	plural map[PluralCategory]string    // non-nil for plural messages
	tmpl   *template.Template           // pre-parsed template (nil if no interpolation)
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
	return t.renderMessage(msg, args)
}

func (t *Translator) renderMessage(msg message, args []any) string {
	if msg.isPlural() {
		return t.renderPlural(msg, args)
	}
	return t.renderPlain(msg, args)
}

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

func (t *Translator) renderPlural(msg message, args []any) string {
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
		cat = Other
		text, ok = msg.plural[Other]
		if !ok {
			// Take any available category.
			for c, v := range msg.plural {
				cat = c
				text = v
				break
			}
		}
	}

	tmpl := msg.ptmpls[cat]
	if tmpl == nil {
		return text
	}

	data := extractData(remaining)
	// Build a new map so we don't mutate the caller's data.
	merged := make(map[string]any, len(data)+1)
	for k, v := range data {
		merged[k] = v
	}
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
