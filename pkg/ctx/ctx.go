// Package ctx provides type-safe context key helpers for Echo handlers.
//
// It wraps Echo's context Set/Get with generics to avoid type assertion
// boilerplate and provides pre-defined keys used across the framework.
package ctx

import "github.com/labstack/echo/v4"

// Key is a type-safe context key for use with Echo's context.
type Key[T any] struct {
	name string
}

// NewKey creates a new typed context key.
func NewKey[T any](name string) Key[T] {
	return Key[T]{name: name}
}

// String returns the key name.
func (k Key[T]) String() string {
	return k.name
}

// Set stores a typed value in the Echo context.
func Set[T any](c echo.Context, key Key[T], value T) {
	c.Set(key.name, value)
}

// Get retrieves a typed value from the Echo context.
//
// A nil context returns (zero, false) rather than panicking. Components render
// outside a request in normal use — middleware.ErrorPages builds its page from
// a func(code int, message string) with no echo.Context to pass down, so the
// scaffold's error page calls @Layout(nil, ...) — and every accessor built on
// Get (GetFlash, GetSubject, GetSubjectID) would otherwise nil-dereference
// there, turning a styled 404 into a recovered panic and a 500.
func Get[T any](c echo.Context, key Key[T]) (T, bool) {
	if c == nil {
		var zero T
		return zero, false
	}
	val := c.Get(key.name)
	if val == nil {
		var zero T
		return zero, false
	}
	typed, ok := val.(T)
	return typed, ok
}

// MustGet retrieves a typed value from the Echo context or panics. Panicking is
// the contract; a nil context says so explicitly rather than surfacing as a bare
// nil dereference from inside Echo.
func MustGet[T any](c echo.Context, key Key[T]) T {
	if c == nil {
		panic("ctx: nil echo.Context for key " + key.name)
	}
	val, ok := Get(c, key)
	if !ok {
		panic("ctx: missing required value for key " + key.name)
	}
	return val
}

// GetAs retrieves a value from the Echo context using an untyped key and
// asserts it to type T. It returns (zero, false) on missing or mismatched type,
// and on a nil context (see Get).
func GetAs[T any](c echo.Context, key Key[any]) (T, bool) {
	if c == nil {
		var zero T
		return zero, false
	}
	val := c.Get(key.name)
	if val == nil {
		var zero T
		return zero, false
	}
	typed, ok := val.(T)
	if !ok {
		var zero T
		return zero, false
	}
	return typed, true
}

// MustGetAs retrieves a value from the Echo context using an untyped key and
// asserts it to type T. It panics with a clear message on missing or mismatched type.
func MustGetAs[T any](c echo.Context, key Key[any]) T {
	if c == nil {
		panic("ctx: nil echo.Context for key " + key.name)
	}
	val, ok := GetAs[T](c, key)
	if !ok {
		panic("ctx: value for key " + key.name + " is missing or not the expected type")
	}
	return val
}

// Pre-defined keys used across the framework.
var (
	SubjectIDKey  = NewKey[string]("subject_id")
	SubjectKey    = NewKey[any]("subject")
	SessionKey    = NewKey[any]("session")
	RequestIDKey  = NewKey[string]("request_id")
	FlashKey      = NewKey[any]("flash")
	TranslatorKey = NewKey[any]("i18n_translator")
	LocaleKey     = NewKey[string]("i18n_locale")
)
