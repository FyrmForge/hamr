package validate_test

import (
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/validate"
)

func TestMinLen(t *testing.T) {
	rule := validate.MinLen(3)
	check(t, "ok", rule("abc"), "")
	check(t, "short", rule("ab"), validate.MsgMinLength)
	check(t, "empty", rule(""), validate.MsgMinLength)
}

func TestMaxLen(t *testing.T) {
	rule := validate.MaxLen(3)
	check(t, "ok", rule("abc"), "")
	check(t, "long", rule("abcd"), validate.MsgMaxLength)
}

func TestIn(t *testing.T) {
	rule := validate.In("a", "b", "c")
	check(t, "found", rule("b"), "")
	check(t, "missing", rule("d"), validate.MsgOneOf)
	check(t, "empty", rule(""), validate.MsgOneOf)
}

func TestAgeMin(t *testing.T) {
	rule := validate.AgeMin(18)
	old := time.Now().AddDate(-20, 0, -1).Format("2006-01-02")
	young := time.Now().AddDate(-17, 0, 0).Format("2006-01-02")
	check(t, "old-enough", rule(old), "")
	check(t, "too-young", rule(young), validate.MsgMinAge)
}

func TestAgeMax(t *testing.T) {
	rule := validate.AgeMax(120)
	recent := time.Now().AddDate(-30, 0, 0).Format("2006-01-02")
	ancient := time.Now().AddDate(-200, 0, 0).Format("2006-01-02")
	check(t, "within", rule(recent), "")
	check(t, "exceeds", rule(ancient), validate.MsgMaxAge)
}
