package validate

// MinLen returns a rule that checks the value has at least n runes.
func MinLen(n int) Rule {
	return func(value string) string {
		return MinLength(value, n)
	}
}

// MaxLen returns a rule that checks the value has at most n runes.
func MaxLen(n int) Rule {
	return func(value string) string {
		return MaxLength(value, n)
	}
}

// In returns a rule that checks the value is one of the allowed options.
func In(options ...string) Rule {
	return func(value string) string {
		return OneOf(value, options...)
	}
}

// AgeMin returns a rule that checks a birth date (YYYY-MM-DD) meets a
// minimum age requirement.
func AgeMin(minAge int) Rule {
	return func(value string) string {
		return MinAge(value, minAge)
	}
}

// AgeMax returns a rule that checks a birth date (YYYY-MM-DD) does not
// exceed a maximum age.
func AgeMax(maxAge int) Rule {
	return func(value string) string {
		return MaxAge(value, maxAge)
	}
}
