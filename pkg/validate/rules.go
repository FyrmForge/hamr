package validate

import "github.com/labstack/echo/v4"

// Rule is a pure validation function: returns "" on success or an error
// message on failure. This is a type alias so existing validators like
// Required and Email satisfy it without casting.
type Rule = func(string) string

// CtxRule is a context-aware validation function that receives the Echo
// context alongside the value. Useful for cross-field comparisons (e.g.
// password confirmation) or checking request-scoped data.
type CtxRule = func(echo.Context, string) string

// WithMsg wraps a rule so that any non-empty result is replaced with msg.
func WithMsg(rule Rule, msg string) Rule {
	return func(value string) string {
		if result := rule(value); result != "" {
			return msg
		}
		return ""
	}
}

// RunRules executes rules in order, returning the first error message.
// Returns "" if all rules pass.
func RunRules(value string, rules ...Rule) string {
	for _, r := range rules {
		if msg := r(value); msg != "" {
			return msg
		}
	}
	return ""
}
