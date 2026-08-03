package validate_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/validate"
)

// helper asserts that got matches want ("" means valid).
func check(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", name, got, want)
	}
}

// ---------------------------------------------------------------------------
// Required
// ---------------------------------------------------------------------------

func TestRequired(t *testing.T) {
	check(t, "non-empty", validate.Required("hello"), "")
	check(t, "empty", validate.Required(""), validate.MsgRequired)
	check(t, "spaces", validate.Required("   "), validate.MsgRequired)
}

func TestRequiredMsg(t *testing.T) {
	check(t, "custom", validate.RequiredMsg("", "fill this in"), "fill this in")
}

// ---------------------------------------------------------------------------
// Email
// ---------------------------------------------------------------------------

func TestEmail(t *testing.T) {
	check(t, "valid", validate.Email("user@example.com"), "")
	check(t, "empty", validate.Email(""), validate.MsgEmailInvalid)
	check(t, "no-at", validate.Email("userexample.com"), validate.MsgEmailInvalid)
	check(t, "no-domain", validate.Email("user@"), validate.MsgEmailInvalid)
	check(t, "no-tld", validate.Email("user@example"), validate.MsgEmailInvalid)
}

// ---------------------------------------------------------------------------
// Phone
// ---------------------------------------------------------------------------

func TestPhone(t *testing.T) {
	check(t, "e164", validate.Phone("+14155551234"), "")
	check(t, "digits", validate.Phone("4155551234"), "")
	check(t, "empty", validate.Phone(""), validate.MsgPhoneInvalid)
	check(t, "too-short", validate.Phone("123"), validate.MsgPhoneInvalid)
	check(t, "letters", validate.Phone("abc1234567"), validate.MsgPhoneInvalid)

	// Formats people actually type.
	check(t, "uk-national-spaced", validate.Phone("07700 900123"), "")
	check(t, "uk-international", validate.Phone("+44 7700 900123"), "")
	check(t, "uk-trunk-zero", validate.Phone("+44 (0)7700 900123"), "")
	check(t, "us-brackets", validate.Phone("(415) 555-1234"), "")
	check(t, "hyphens", validate.Phone("0161-496-0000"), "")
	check(t, "dots", validate.Phone("+1.415.555.1234"), "")
	check(t, "padded", validate.Phone("  4155551234  "), "")
	check(t, "unicode-dash", validate.Phone("0161\u2011496\u20110000"), "")

	// Separator tolerance must not become a hole.
	check(t, "separators-only", validate.Phone("-- () --"), validate.MsgPhoneInvalid)
	check(t, "zero-country-code", validate.Phone("+0000000"), validate.MsgPhoneInvalid)
	check(t, "interior-plus", validate.Phone("44+7700900123"), validate.MsgPhoneInvalid)
	check(t, "too-long", validate.Phone("+1234567890123456"), validate.MsgPhoneInvalid)
	check(t, "letters-formatted", validate.Phone("(abc) 555-1234"), validate.MsgPhoneInvalid)
	// Extensions are out of scope: the number must stand on its own.
	check(t, "extension", validate.Phone("4155551234 x123"), validate.MsgPhoneInvalid)
}

// ---------------------------------------------------------------------------
// URL
// ---------------------------------------------------------------------------

func TestURL(t *testing.T) {
	check(t, "https", validate.URL("https://example.com"), "")
	check(t, "http", validate.URL("http://example.com/path?q=1"), "")
	check(t, "empty", validate.URL(""), validate.MsgURLInvalid)
	check(t, "no-scheme", validate.URL("example.com"), validate.MsgURLInvalid)
	check(t, "bare", validate.URL("not a url"), validate.MsgURLInvalid)
	// Non-web schemes are rejected — they're XSS vectors in href/src.
	check(t, "javascript-host", validate.URL("javascript://x/%0aalert(1)"), validate.MsgURLInvalid)
	check(t, "javascript-bare", validate.URL("javascript:alert(1)"), validate.MsgURLInvalid)
	check(t, "data", validate.URL("data:text/html,<script>alert(1)</script>"), validate.MsgURLInvalid)
	check(t, "vbscript", validate.URL("vbscript:msgbox(1)"), validate.MsgURLInvalid)
}

// ---------------------------------------------------------------------------
// MinLength / MaxLength
// ---------------------------------------------------------------------------

func TestMinLength(t *testing.T) {
	check(t, "ok", validate.MinLength("abc", 3), "")
	check(t, "short", validate.MinLength("ab", 3), validate.MsgMinLength)
}

func TestMaxLength(t *testing.T) {
	check(t, "ok", validate.MaxLength("abc", 3), "")
	check(t, "long", validate.MaxLength("abcd", 3), validate.MsgMaxLength)
}

// ---------------------------------------------------------------------------
// OneOf
// ---------------------------------------------------------------------------

func TestOneOf(t *testing.T) {
	check(t, "found", validate.OneOf("b", "a", "b", "c"), "")
	check(t, "missing", validate.OneOf("d", "a", "b", "c"), validate.MsgOneOf)
	check(t, "empty", validate.OneOf("", "a", "b"), validate.MsgOneOf)
}

// ---------------------------------------------------------------------------
// IntRange
// ---------------------------------------------------------------------------

func TestIntRange(t *testing.T) {
	check(t, "in-range", validate.IntRange(5, 1, 10), "")
	check(t, "below", validate.IntRange(0, 1, 10), validate.MsgIntRange)
	check(t, "above", validate.IntRange(11, 1, 10), validate.MsgIntRange)
	check(t, "edge-low", validate.IntRange(1, 1, 10), "")
	check(t, "edge-high", validate.IntRange(10, 1, 10), "")
}

// ---------------------------------------------------------------------------
// MinAge / MaxAge
// ---------------------------------------------------------------------------

func TestMinAge(t *testing.T) {
	old := time.Now().AddDate(-20, 0, -1).Format("2006-01-02")
	young := time.Now().AddDate(-17, 0, 0).Format("2006-01-02")

	check(t, "old-enough", validate.MinAge(old, 18), "")
	check(t, "too-young", validate.MinAge(young, 18), validate.MsgMinAge)
	check(t, "empty", validate.MinAge("", 18), validate.MsgMinAge)
	check(t, "bad-date", validate.MinAge("not-a-date", 18), validate.MsgMinAge)
}

func TestMaxAge(t *testing.T) {
	recent := time.Now().AddDate(-30, 0, 0).Format("2006-01-02")
	ancient := time.Now().AddDate(-200, 0, 0).Format("2006-01-02")

	check(t, "within", validate.MaxAge(recent, 120), "")
	check(t, "exceeds", validate.MaxAge(ancient, 120), validate.MsgMaxAge)
	check(t, "empty", validate.MaxAge("", 120), validate.MsgMaxAge)
}

// ---------------------------------------------------------------------------
// EmptyOr
// ---------------------------------------------------------------------------

func TestEmptyOr(t *testing.T) {
	check(t, "empty-string", validate.EmptyOr(validate.Email)(""), "")
	check(t, "whitespace", validate.EmptyOr(validate.Email)("   "), "")
	check(t, "invalid", validate.EmptyOr(validate.Email)("bad"), validate.MsgEmailInvalid)
	check(t, "valid", validate.EmptyOr(validate.Email)("good@example.com"), "")
}

// ---------------------------------------------------------------------------
// PasswordStrength
// ---------------------------------------------------------------------------

func TestPasswordStrength(t *testing.T) {
	check(t, "strong", validate.PasswordStrength("Str0ng!Pw"), "")
	check(t, "weak-short", validate.PasswordStrength("Sh0!"), validate.MsgPasswordWeak)
	check(t, "no-upper", validate.PasswordStrength("str0ng!pw"), validate.MsgPasswordWeak)
	check(t, "no-lower", validate.PasswordStrength("STR0NG!PW"), validate.MsgPasswordWeak)
	check(t, "no-digit", validate.PasswordStrength("Strong!Pw"), validate.MsgPasswordWeak)
	check(t, "no-special", validate.PasswordStrength("Str0ngPwd"), validate.MsgPasswordWeak)
}

func TestCheckPasswordRequirements(t *testing.T) {
	reqs := validate.CheckPasswordRequirements("Str0ng!Pw")
	if len(reqs) != 5 {
		t.Fatalf("expected 5 requirements, got %d", len(reqs))
	}
	for _, r := range reqs {
		if !r.Met {
			t.Errorf("requirement %q should be met", r.Description)
		}
	}
}

// ---------------------------------------------------------------------------
// HasUpper / HasLower / HasDigit / HasSpecial
// ---------------------------------------------------------------------------

func TestHasUpper(t *testing.T) {
	check(t, "has-upper", validate.HasUpper("helloA"), "")
	check(t, "no-upper", validate.HasUpper("hello"), validate.MsgHasUpper)
	check(t, "empty", validate.HasUpper(""), validate.MsgHasUpper)
	check(t, "digits-only", validate.HasUpper("12345"), validate.MsgHasUpper)
}

func TestHasUpperMsg(t *testing.T) {
	check(t, "custom", validate.HasUpperMsg("abc", "need upper"), "need upper")
	check(t, "custom-pass", validate.HasUpperMsg("Abc", "need upper"), "")
}

func TestHasLower(t *testing.T) {
	check(t, "has-lower", validate.HasLower("HELLOa"), "")
	check(t, "no-lower", validate.HasLower("HELLO"), validate.MsgHasLower)
	check(t, "empty", validate.HasLower(""), validate.MsgHasLower)
	check(t, "digits-only", validate.HasLower("12345"), validate.MsgHasLower)
}

func TestHasLowerMsg(t *testing.T) {
	check(t, "custom", validate.HasLowerMsg("ABC", "need lower"), "need lower")
	check(t, "custom-pass", validate.HasLowerMsg("ABc", "need lower"), "")
}

func TestHasDigit(t *testing.T) {
	check(t, "has-digit", validate.HasDigit("abc1"), "")
	check(t, "no-digit", validate.HasDigit("abc"), validate.MsgHasDigit)
	check(t, "empty", validate.HasDigit(""), validate.MsgHasDigit)
	check(t, "only-digit", validate.HasDigit("5"), "")
}

func TestHasDigitMsg(t *testing.T) {
	check(t, "custom", validate.HasDigitMsg("abc", "need digit"), "need digit")
	check(t, "custom-pass", validate.HasDigitMsg("ab1", "need digit"), "")
}

func TestHasSpecial(t *testing.T) {
	check(t, "has-special", validate.HasSpecial("abc!"), "")
	check(t, "no-special", validate.HasSpecial("abc123"), validate.MsgHasSpecial)
	check(t, "empty", validate.HasSpecial(""), validate.MsgHasSpecial)
	check(t, "symbol", validate.HasSpecial("abc$"), "")
}

func TestHasSpecialMsg(t *testing.T) {
	check(t, "custom", validate.HasSpecialMsg("abc", "need special"), "need special")
	check(t, "custom-pass", validate.HasSpecialMsg("a@b", "need special"), "")
}

// ---------------------------------------------------------------------------
// NormalizeURL
// ---------------------------------------------------------------------------

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"example.com", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"http://example.com", "http://example.com"},
	}
	for _, tt := range tests {
		if got := validate.NormalizeURL(tt.in); got != tt.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// NormalizePhone
// ---------------------------------------------------------------------------

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"07700 900123", "07700900123"},
		{"+44 7700 900123", "+447700900123"},
		{"(415) 555-1234", "4155551234"},
		{"  +1-415-555-1234 ", "+14155551234"},
		// Trunk zero only drops in international form; a national number keeps
		// it and loses just the brackets.
		{"+44 (0)7700 900123", "+447700900123"},
		{"(0)7700900123", "07700900123"},
		// Non-separator characters survive, so invalid input stays invalid.
		{"abc", "abc"},
		{"44+7700900123", "44+7700900123"},
	}
	for _, tt := range tests {
		if got := validate.NormalizePhone(tt.in); got != tt.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Custom validator registry
// ---------------------------------------------------------------------------

func TestRegisterAndRun(t *testing.T) {
	validate.Register("nonempty", func(v string) string {
		if v == "" {
			return "must not be empty"
		}
		return ""
	})

	check(t, "registered-valid", validate.Run("nonempty", "hi"), "")
	check(t, "registered-invalid", validate.Run("nonempty", ""), "must not be empty")
}

func TestRun_unknown(t *testing.T) {
	got := validate.Run("doesnotexist", "x")
	want := fmt.Sprintf("%s: doesnotexist", validate.MsgValidatorNotFound)
	if got != want {
		t.Errorf("Run(unknown) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// *Msg variants
// ---------------------------------------------------------------------------

func TestCustomMessages(t *testing.T) {
	check(t, "EmailMsg", validate.EmailMsg("bad", "custom"), "custom")
	check(t, "PhoneMsg", validate.PhoneMsg("bad", "custom"), "custom")
	check(t, "URLMsg", validate.URLMsg("bad", "custom"), "custom")
	check(t, "MinLengthMsg", validate.MinLengthMsg("a", 5, "custom"), "custom")
	check(t, "MaxLengthMsg", validate.MaxLengthMsg("abcdef", 3, "custom"), "custom")
	check(t, "OneOfMsg", validate.OneOfMsg("x", "custom", "a", "b"), "custom")
	check(t, "IntRangeMsg", validate.IntRangeMsg(0, 1, 10, "custom"), "custom")
	check(t, "PasswordStrengthMsg", validate.PasswordStrengthMsg("weak", "custom"), "custom")
	check(t, "HasUpperMsg", validate.HasUpperMsg("abc", "custom"), "custom")
	check(t, "HasLowerMsg", validate.HasLowerMsg("ABC", "custom"), "custom")
	check(t, "HasDigitMsg", validate.HasDigitMsg("abc", "custom"), "custom")
	check(t, "HasSpecialMsg", validate.HasSpecialMsg("abc", "custom"), "custom")
}
