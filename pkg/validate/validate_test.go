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
	check(t, "empty", validate.Email(""), "")
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
	check(t, "empty", validate.Phone(""), "")
	check(t, "too-short", validate.Phone("123"), validate.MsgPhoneInvalid)
	check(t, "letters", validate.Phone("abc1234567"), validate.MsgPhoneInvalid)
}

// ---------------------------------------------------------------------------
// URL
// ---------------------------------------------------------------------------

func TestURL(t *testing.T) {
	check(t, "https", validate.URL("https://example.com"), "")
	check(t, "empty", validate.URL(""), "")
	check(t, "no-scheme", validate.URL("example.com"), validate.MsgURLInvalid)
	check(t, "bare", validate.URL("not a url"), validate.MsgURLInvalid)
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
// Match
// ---------------------------------------------------------------------------

func TestMatch(t *testing.T) {
	check(t, "match", validate.Match("abc123", `^[a-z]+[0-9]+$`), "")
	check(t, "no-match", validate.Match("ABC", `^[a-z]+$`), validate.MsgPatternMismatch)
	check(t, "empty", validate.Match("", `^[a-z]+$`), "")
	check(t, "bad-pattern", validate.Match("abc", `[`), validate.MsgPatternMismatch)
}

// ---------------------------------------------------------------------------
// OneOf
// ---------------------------------------------------------------------------

func TestOneOf(t *testing.T) {
	check(t, "found", validate.OneOf("b", "a", "b", "c"), "")
	check(t, "missing", validate.OneOf("d", "a", "b", "c"), validate.MsgOneOf)
	check(t, "empty", validate.OneOf("", "a", "b"), "")
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
	check(t, "empty", validate.MinAge("", 18), "")
	check(t, "bad-date", validate.MinAge("not-a-date", 18), validate.MsgMinAge)
}

func TestMaxAge(t *testing.T) {
	recent := time.Now().AddDate(-30, 0, 0).Format("2006-01-02")
	ancient := time.Now().AddDate(-200, 0, 0).Format("2006-01-02")

	check(t, "within", validate.MaxAge(recent, 120), "")
	check(t, "exceeds", validate.MaxAge(ancient, 120), validate.MsgMaxAge)
	check(t, "empty", validate.MaxAge("", 120), "")
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
	check(t, "MatchMsg", validate.MatchMsg("abc", `^[0-9]+$`, "custom"), "custom")
	check(t, "OneOfMsg", validate.OneOfMsg("x", "custom", "a", "b"), "custom")
	check(t, "IntRangeMsg", validate.IntRangeMsg(0, 1, 10, "custom"), "custom")
	check(t, "PasswordStrengthMsg", validate.PasswordStrengthMsg("weak", "custom"), "custom")
}
