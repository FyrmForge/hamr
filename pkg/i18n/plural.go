// Package i18n provides internationalisation support: JSON translation files,
// CLDR plural rules, text/template interpolation, and a Bundle that holds
// per-locale Translators.
package i18n

import (
	"strings"
	"sync"
)

// PluralCategory represents a CLDR plural category.
type PluralCategory string

const (
	Zero  PluralCategory = "zero"
	One   PluralCategory = "one"
	Two   PluralCategory = "two"
	Few   PluralCategory = "few"
	Many  PluralCategory = "many"
	Other PluralCategory = "other"
)

// validPluralCategories is the set of valid CLDR categories for detecting
// plural objects in JSON.
var validPluralCategories = map[string]PluralCategory{
	"zero":  Zero,
	"one":   One,
	"two":   Two,
	"few":   Few,
	"many":  Many,
	"other": Other,
}

// PluralRule maps a count to its CLDR plural category for a language.
type PluralRule func(n int) PluralCategory

// builtinRules contains plural rules for common languages.
var builtinRules = map[string]PluralRule{
	// Germanic / Romance (one vs other)
	"en": pluralOneOther,
	"de": pluralOneOther,
	"nl": pluralOneOther,
	"sv": pluralOneOther,
	"da": pluralOneOther,
	"no": pluralOneOther,
	"nb": pluralOneOther,
	"nn": pluralOneOther,
	"es": pluralOneOther,
	"it": pluralOneOther,
	"pt": pluralOneOther,
	"el": pluralOneOther,
	"fi": pluralOneOther,
	"hu": pluralOneOther,
	"tr": pluralOneOther,
	"bg": pluralOneOther,
	"hi": pluralOneOther,

	// French: 0 and 1 are "one"
	"fr": func(n int) PluralCategory {
		if n == 0 || n == 1 {
			return One
		}
		return Other
	},

	// Polish
	"pl": func(n int) PluralCategory {
		mod10 := n % 10
		mod100 := n % 100
		if n == 1 {
			return One
		}
		if mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
			return Few
		}
		if (mod10 == 0 || mod10 == 1) || (mod10 >= 5 && mod10 <= 9) || (mod100 >= 12 && mod100 <= 14) {
			return Many
		}
		return Other
	},

	// Russian / Ukrainian
	"ru": pluralSlavic,
	"uk": pluralSlavic,

	// Czech / Slovak
	"cs": func(n int) PluralCategory {
		if n == 1 {
			return One
		}
		if n >= 2 && n <= 4 {
			return Few
		}
		return Other
	},
	"sk": func(n int) PluralCategory {
		if n == 1 {
			return One
		}
		if n >= 2 && n <= 4 {
			return Few
		}
		return Other
	},

	// Arabic
	"ar": func(n int) PluralCategory {
		if n == 0 {
			return Zero
		}
		if n == 1 {
			return One
		}
		if n == 2 {
			return Two
		}
		mod100 := n % 100
		if mod100 >= 3 && mod100 <= 10 {
			return Few
		}
		if mod100 >= 11 && mod100 <= 99 {
			return Many
		}
		return Other
	},

	// East Asian (no plural forms)
	"ja": pluralOtherOnly,
	"zh": pluralOtherOnly,
	"ko": pluralOtherOnly,
	"vi": pluralOtherOnly,
	"th": pluralOtherOnly,
	"id": pluralOtherOnly,
	"ms": pluralOtherOnly,
}

func pluralOneOther(n int) PluralCategory {
	if n == 1 {
		return One
	}
	return Other
}

func pluralOtherOnly(_ int) PluralCategory {
	return Other
}

func pluralSlavic(n int) PluralCategory {
	mod10 := n % 10
	mod100 := n % 100
	if mod10 == 1 && mod100 != 11 {
		return One
	}
	if mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
		return Few
	}
	if mod10 == 0 || (mod10 >= 5 && mod10 <= 9) || (mod100 >= 11 && mod100 <= 14) {
		return Many
	}
	return Other
}

var (
	customRulesMu sync.RWMutex
	customRules   = map[string]PluralRule{}
)

// RegisterRule adds or overrides a plural rule for the given language tag.
func RegisterRule(lang string, rule PluralRule) {
	customRulesMu.Lock()
	customRules[lang] = rule
	customRulesMu.Unlock()
}

// RuleFor returns the plural rule for a language. It tries the full tag
// first (e.g. "fr-CA"), then the base language ("fr"), then custom rules,
// then builtins, falling back to English.
func RuleFor(lang string) PluralRule {
	customRulesMu.RLock()
	if r, ok := customRules[lang]; ok {
		customRulesMu.RUnlock()
		return r
	}
	customRulesMu.RUnlock()
	if r, ok := builtinRules[lang]; ok {
		return r
	}
	// Try base language before hyphen (e.g. "fr-CA" → "fr").
	if base, _, ok := strings.Cut(lang, "-"); ok {
		customRulesMu.RLock()
		if r, ok := customRules[base]; ok {
			customRulesMu.RUnlock()
			return r
		}
		customRulesMu.RUnlock()
		if r, ok := builtinRules[base]; ok {
			return r
		}
	}
	return builtinRules["en"]
}
