package validate_test

import (
	"testing"

	"github.com/FyrmForge/hamr/pkg/validate"
)

func TestWithMsg(t *testing.T) {
	custom := validate.WithMsg(validate.Email, "bad email")

	check(t, "passes", custom("user@example.com"), "")
	check(t, "overrides", custom("bad"), "bad email")
}

func TestRunRules(t *testing.T) {
	check(t, "all-pass", validate.RunRules("user@example.com", validate.Required, validate.Email), "")
	check(t, "first-fails", validate.RunRules("", validate.Required, validate.Email), validate.MsgRequired)
	check(t, "second-fails", validate.RunRules("bad", validate.Required, validate.Email), validate.MsgEmailInvalid)
	check(t, "no-rules", validate.RunRules("anything"), "")
}
