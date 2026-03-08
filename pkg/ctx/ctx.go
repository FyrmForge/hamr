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
func Get[T any](c echo.Context, key Key[T]) (T, bool) {
	val := c.Get(key.name)
	if val == nil {
		var zero T
		return zero, false
	}
	typed, ok := val.(T)
	return typed, ok
}

// MustGet retrieves a typed value from the Echo context or panics.
func MustGet[T any](c echo.Context, key Key[T]) T {
	val, ok := Get(c, key)
	if !ok {
		panic("ctx: missing required value for key " + key.name)
	}
	return val
}

// GetAs retrieves a value from the Echo context using an untyped key and
// asserts it to type T. It returns (zero, false) on missing or mismatched type.
func GetAs[T any](c echo.Context, key Key[any]) (T, bool) {
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
	val, ok := GetAs[T](c, key)
	if !ok {
		panic("ctx: value for key " + key.name + " is missing or not the expected type")
	}
	return val
}

// Pre-defined keys used across the framework.
var (
	SubjectIDKey = NewKey[string]("subject_id")
	SubjectKey   = NewKey[any]("subject")
	SessionKey   = NewKey[any]("session")
	RequestIDKey = NewKey[string]("request_id")
	FlashKey     = NewKey[any]("flash")
)
