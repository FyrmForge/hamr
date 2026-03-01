// Package ptr provides generic and concrete helpers for working with pointers.
//
// The generic functions To, From, and FromOr cover any type. Concrete helpers
// like String, Int, and Bool are provided for the most common stdlib types.
// Conversion helpers IntToStr and BoolToYesNo turn pointer values into
// human-readable strings.
package ptr

import "strconv"

// To returns a pointer to v.
func To[T any](v T) *T {
	return &v
}

// From dereferences p, returning the zero value of T when p is nil.
func From[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// FromOr dereferences p, returning def when p is nil.
func FromOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// String dereferences a *string, returning "" when nil.
func String(p *string) string { return From(p) }

// Int dereferences a *int, returning 0 when nil.
func Int(p *int) int { return From(p) }

// Bool dereferences a *bool, returning false when nil.
func Bool(p *bool) bool { return From(p) }

// IntToStr converts a *int to its string representation, returning "" when nil.
func IntToStr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

// BoolToYesNo converts a *bool to "Yes" or "No", returning "" when nil.
func BoolToYesNo(p *bool) string {
	if p == nil {
		return ""
	}
	if *p {
		return "Yes"
	}
	return "No"
}
