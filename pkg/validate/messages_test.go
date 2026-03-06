package validate_test

import (
	"testing"

	"github.com/FyrmForge/hamr/pkg/validate"
)

func TestMessageConstants(t *testing.T) {
	// Ensure all exported message constants are non-empty.
	msgs := []string{
		validate.MsgRequired,
		validate.MsgEmailInvalid,
		validate.MsgPhoneInvalid,
		validate.MsgURLInvalid,
		validate.MsgMinLength,
		validate.MsgMaxLength,
		validate.MsgOneOf,
		validate.MsgIntRange,
		validate.MsgMinAge,
		validate.MsgMaxAge,
		validate.MsgPasswordWeak,
		validate.MsgValidatorNotFound,
	}
	for i, m := range msgs {
		if m == "" {
			t.Errorf("message constant at index %d is empty", i)
		}
	}
}
