package i18n

import "strings"

// rtlLanguages lists language codes whose script direction is right-to-left.
var rtlLanguages = map[string]bool{
	"ar": true,
	"he": true,
	"fa": true,
	"ur": true,
	"ps": true,
	"sd": true,
	"yi": true,
	"ku": true,
}

// DirectionFor returns "rtl" for right-to-left languages and "ltr" otherwise.
// Supports region tags like "ar-SA" by checking the base language.
func DirectionFor(lang string) string {
	if rtlLanguages[lang] {
		return "rtl"
	}
	// Try base language (e.g. "ar-SA" → "ar").
	if base, _, ok := strings.Cut(lang, "-"); ok && rtlLanguages[base] {
		return "rtl"
	}
	return "ltr"
}
