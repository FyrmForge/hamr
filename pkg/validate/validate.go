package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ---------------------------------------------------------------------------
// Core validators
// ---------------------------------------------------------------------------

// Required returns "" if value is non-empty, or MsgRequired.
func Required(value string) string {
	return RequiredMsg(value, MsgRequired)
}

// RequiredMsg is like Required with a custom message.
func RequiredMsg(value, msg string) string {
	if strings.TrimSpace(value) == "" {
		return msg
	}
	return ""
}

// EmptyOr wraps a validator function so that empty (whitespace-only) values
// pass validation. Use this when a field is optional but must be valid if
// provided.
func EmptyOr(fn func(string) string) func(string) string {
	return func(value string) string {
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return fn(value)
	}
}

// Email returns "" if value looks like a valid email, or MsgEmailInvalid.
func Email(value string) string {
	return EmailMsg(value, MsgEmailInvalid)
}

// EmailMsg is like Email with a custom message.
func EmailMsg(value, msg string) string {
	if !emailRe.MatchString(value) {
		return msg
	}
	return ""
}

// Phone returns "" if value is a plausible phone number, or MsgPhoneInvalid.
// It accepts optional leading '+' followed by 7-15 digits.
func Phone(value string) string {
	return PhoneMsg(value, MsgPhoneInvalid)
}

// PhoneMsg is like Phone with a custom message.
func PhoneMsg(value, msg string) string {
	if !phoneRe.MatchString(value) {
		return msg
	}
	return ""
}

// URL returns "" if value is a valid absolute URL, or MsgURLInvalid.
func URL(value string) string {
	return URLMsg(value, MsgURLInvalid)
}

// URLMsg is like URL with a custom message. Only http/https URLs are accepted:
// a validated URL frequently lands in an href/src, and schemes like
// javascript:, data:, and vbscript: are XSS vectors. Note "javascript://x/..."
// parses with both a scheme and a host, so a scheme allowlist (not just a
// non-empty-scheme check) is required to reject it.
func URLMsg(value, msg string) string {
	u, err := url.ParseRequestURI(value)
	if err != nil || u.Host == "" {
		return msg
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return ""
	default:
		return msg
	}
}

// MinLength returns "" if value has at least min runes, or MsgMinLength.
func MinLength(value string, min int) string {
	return MinLengthMsg(value, min, MsgMinLength)
}

// MinLengthMsg is like MinLength with a custom message.
func MinLengthMsg(value string, min int, msg string) string {
	if len([]rune(value)) < min {
		return msg
	}
	return ""
}

// MaxLength returns "" if value has at most max runes, or MsgMaxLength.
func MaxLength(value string, max int) string {
	return MaxLengthMsg(value, max, MsgMaxLength)
}

// MaxLengthMsg is like MaxLength with a custom message.
func MaxLengthMsg(value string, max int, msg string) string {
	if len([]rune(value)) > max {
		return msg
	}
	return ""
}

// OneOf returns "" if value is one of the allowed options, or MsgOneOf.
func OneOf(value string, options ...string) string {
	return OneOfMsg(value, MsgOneOf, options...)
}

// OneOfMsg is like OneOf with a custom message.
func OneOfMsg(value, msg string, options ...string) string {
	if slices.Contains(options, value) {
		return ""
	}
	return msg
}

// IntRange returns "" if value is between min and max (inclusive), or
// MsgIntRange.
func IntRange(value int, min, max int) string {
	return IntRangeMsg(value, min, max, MsgIntRange)
}

// IntRangeMsg is like IntRange with a custom message.
func IntRangeMsg(value, min, max int, msg string) string {
	if value < min || value > max {
		return msg
	}
	return ""
}

// ---------------------------------------------------------------------------
// Date / age validators
// ---------------------------------------------------------------------------

const dateLayout = "2006-01-02"

// MinAge returns "" if the person with the given birthDate (YYYY-MM-DD) is at
// least minAge years old, or MsgMinAge.
func MinAge(birthDate string, minAge int) string {
	return MinAgeMsg(birthDate, minAge, MsgMinAge)
}

// MinAgeMsg is like MinAge with a custom message.
func MinAgeMsg(birthDate string, minAge int, msg string) string {
	dob, err := time.Parse(dateLayout, birthDate)
	if err != nil {
		return msg
	}
	// time.Parse yields a UTC instant; compare against "now" in UTC too, or a
	// local-zone now would shift the cutoff by up to a day near midnight.
	cutoff := time.Now().UTC().AddDate(-minAge, 0, 0)
	if dob.After(cutoff) {
		return msg
	}
	return ""
}

// MaxAge returns "" if the person with the given birthDate (YYYY-MM-DD) is at
// most maxAge years old, or MsgMaxAge.
func MaxAge(birthDate string, maxAge int) string {
	return MaxAgeMsg(birthDate, maxAge, MsgMaxAge)
}

// MaxAgeMsg is like MaxAge with a custom message.
func MaxAgeMsg(birthDate string, maxAge int, msg string) string {
	dob, err := time.Parse(dateLayout, birthDate)
	if err != nil {
		return msg
	}
	// UTC to match the UTC-parsed dob (see MinAgeMsg).
	cutoff := time.Now().UTC().AddDate(-(maxAge + 1), 0, 0)
	if dob.Before(cutoff) {
		return msg
	}
	return ""
}

// ---------------------------------------------------------------------------
// Password strength
// ---------------------------------------------------------------------------

// PasswordRequirement describes a single password rule and whether it is met.
type PasswordRequirement struct {
	Description string
	Met         bool
}

// CheckPasswordRequirements evaluates password against common strength rules
// and returns the status of each requirement.
func CheckPasswordRequirements(password string) []PasswordRequirement {
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}
	return []PasswordRequirement{
		{"At least 8 characters", len([]rune(password)) >= 8},
		{"At least one uppercase letter", hasUpper},
		{"At least one lowercase letter", hasLower},
		{"At least one digit", hasDigit},
		{"At least one special character", hasSpecial},
	}
}

// PasswordStrength returns "" if all password requirements are met, or
// MsgPasswordWeak.
func PasswordStrength(password string) string {
	return PasswordStrengthMsg(password, MsgPasswordWeak)
}

// PasswordStrengthMsg is like PasswordStrength with a custom message.
func PasswordStrengthMsg(password, msg string) string {
	for _, req := range CheckPasswordRequirements(password) {
		if !req.Met {
			return msg
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Individual character-class validators
// ---------------------------------------------------------------------------

// HasUpper returns "" if value contains at least one uppercase letter, or
// MsgHasUpper.
func HasUpper(value string) string {
	return HasUpperMsg(value, MsgHasUpper)
}

// HasUpperMsg is like HasUpper with a custom message.
func HasUpperMsg(value, msg string) string {
	for _, r := range value {
		if unicode.IsUpper(r) {
			return ""
		}
	}
	return msg
}

// HasLower returns "" if value contains at least one lowercase letter, or
// MsgHasLower.
func HasLower(value string) string {
	return HasLowerMsg(value, MsgHasLower)
}

// HasLowerMsg is like HasLower with a custom message.
func HasLowerMsg(value, msg string) string {
	for _, r := range value {
		if unicode.IsLower(r) {
			return ""
		}
	}
	return msg
}

// HasDigit returns "" if value contains at least one digit, or MsgHasDigit.
func HasDigit(value string) string {
	return HasDigitMsg(value, MsgHasDigit)
}

// HasDigitMsg is like HasDigit with a custom message.
func HasDigitMsg(value, msg string) string {
	for _, r := range value {
		if unicode.IsDigit(r) {
			return ""
		}
	}
	return msg
}

// HasSpecial returns "" if value contains at least one special character
// (punctuation or symbol), or MsgHasSpecial.
func HasSpecial(value string) string {
	return HasSpecialMsg(value, MsgHasSpecial)
}

// HasSpecialMsg is like HasSpecial with a custom message.
func HasSpecialMsg(value, msg string) string {
	for _, r := range value {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return ""
		}
	}
	return msg
}

// ---------------------------------------------------------------------------
// URL normalisation
// ---------------------------------------------------------------------------

// NormalizeURL prepends "https://" when value has no scheme. Returns the
// original value unchanged if it is empty or already has a scheme.
func NormalizeURL(value string) string {
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		return "https://" + value
	}
	return value
}

// ---------------------------------------------------------------------------
// Custom validator registry
// ---------------------------------------------------------------------------

var (
	registryMu sync.RWMutex
	registry   = map[string]func(string) string{}
)

// Register adds a named validator function to the global registry.
func Register(name string, fn func(string) string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = fn
}

// Run executes a registered validator by name. If the name is not found it
// returns MsgValidatorNotFound.
func Run(name, value string) string {
	registryMu.RLock()
	fn, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return fmt.Sprintf("%s: %s", MsgValidatorNotFound, name)
	}
	return fn(value)
}

// ---------------------------------------------------------------------------
// Compiled regexes
// ---------------------------------------------------------------------------

var (
	emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	phoneRe = regexp.MustCompile(`^\+?[0-9]{7,15}$`)
)
