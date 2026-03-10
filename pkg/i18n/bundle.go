package i18n

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BundleConfig configures how a Bundle loads locale files.
type BundleConfig struct {
	LocaleDir         string // directory containing *.json files
	DefaultLocale     string // e.g. "en"
	FallbackToDefault *bool  // fall back to default locale for missing keys (default true; nil = true)
}


// Bundle holds translators for all loaded locales.
type Bundle struct {
	translators    map[string]*Translator
	defaultLocale  string
	supported      []string
}

// NewBundle reads all *.json files from cfg.LocaleDir, validates them against
// the default locale, and returns a ready-to-use Bundle.
func NewBundle(cfg BundleConfig) (*Bundle, error) {
	if cfg.LocaleDir == "" {
		return nil, fmt.Errorf("i18n: LocaleDir is required")
	}
	if cfg.DefaultLocale == "" {
		cfg.DefaultLocale = "en"
	}

	entries, err := os.ReadDir(cfg.LocaleDir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read locale dir %s: %w", cfg.LocaleDir, err)
	}

	// Load all locale files.
	type localeData struct {
		name     string
		raw      map[string]any
		messages map[string]message
		meta     *localeMeta
	}
	var locales []localeData

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		path := filepath.Join(cfg.LocaleDir, e.Name())
		raw, err := loadJSON(path)
		if err != nil {
			return nil, err
		}
		msgs, err := flattenMessages(raw, "")
		if err != nil {
			return nil, fmt.Errorf("locale %s: %w", name, err)
		}
		meta := extractMeta(raw)
		locales = append(locales, localeData{
			name:     name,
			raw:      raw,
			messages: msgs,
			meta:     meta,
		})
	}

	if len(locales) == 0 {
		return nil, fmt.Errorf("i18n: no locale files found in %s", cfg.LocaleDir)
	}

	// Find default locale.
	var defaultData *localeData
	for i := range locales {
		if locales[i].name == cfg.DefaultLocale {
			defaultData = &locales[i]
			break
		}
	}
	if defaultData == nil {
		return nil, fmt.Errorf("i18n: default locale %q not found in %s", cfg.DefaultLocale, cfg.LocaleDir)
	}

	// Validate non-default locales against default.
	for _, ld := range locales {
		if ld.name == cfg.DefaultLocale {
			continue
		}
		for _, ve := range validateLocale(defaultData.messages, ld.messages, ld.name) {
			if strings.Contains(ve.Message, "interpolation mismatch") {
				return nil, fmt.Errorf("i18n: %s", ve.Error())
			}
		}
	}

	// Build default translator first (no fallback).
	defaultDir := directionForLocale(cfg.DefaultLocale, defaultData.meta)
	defaultTranslator := newTranslator(cfg.DefaultLocale, defaultData.messages, defaultDir, nil)

	b := &Bundle{
		translators:   make(map[string]*Translator, len(locales)),
		defaultLocale: cfg.DefaultLocale,
	}
	b.translators[cfg.DefaultLocale] = defaultTranslator

	// Build other translators.
	for _, ld := range locales {
		if ld.name == cfg.DefaultLocale {
			continue
		}
		dir := directionForLocale(ld.name, ld.meta)
		var fb *Translator
		if cfg.FallbackToDefault == nil || *cfg.FallbackToDefault {
			fb = defaultTranslator
		}
		b.translators[ld.name] = newTranslator(ld.name, ld.messages, dir, fb)
	}

	// Build sorted supported list.
	for name := range b.translators {
		b.supported = append(b.supported, name)
	}
	sort.Strings(b.supported)

	return b, nil
}

// Translator returns the Translator for a locale, falling back to the default.
func (b *Bundle) Translator(locale string) *Translator {
	if t, ok := b.translators[locale]; ok {
		return t
	}
	return b.translators[b.defaultLocale]
}

// SupportedLocales returns the sorted list of loaded locale codes.
func (b *Bundle) SupportedLocales() []string {
	return b.supported
}

// HasLocale reports whether the given locale is loaded.
func (b *Bundle) HasLocale(locale string) bool {
	_, ok := b.translators[locale]
	return ok
}

// ResolveLocale returns the best matching loaded locale for a code that may
// include a region tag (e.g. "fr-FR"). It tries an exact match first, then
// the base language. Returns ("", false) if no match is found.
func (b *Bundle) ResolveLocale(code string) (string, bool) {
	if _, ok := b.translators[code]; ok {
		return code, true
	}
	if base, _, ok := strings.Cut(code, "-"); ok {
		if _, ok := b.translators[base]; ok {
			return base, true
		}
	}
	return "", false
}

// DefaultLocale returns the default locale code.
func (b *Bundle) DefaultLocale() string {
	return b.defaultLocale
}

func directionForLocale(lang string, meta *localeMeta) string {
	if meta != nil && meta.Direction != "" {
		return meta.Direction
	}
	return DirectionFor(lang)
}
