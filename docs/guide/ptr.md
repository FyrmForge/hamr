# Ptr — Pointer Helpers

`hamr/pkg/ptr` provides generic and concrete helpers for working with pointers.
Useful when building structs with optional fields or working with database nullable
columns. Zero dependencies.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/ptr"
```

## Generic Helpers

### To — create a pointer

```go
name := ptr.To("Alice")    // *string pointing to "Alice"
age  := ptr.To(30)         // *int pointing to 30
ok   := ptr.To(true)       // *bool pointing to true
```

Useful for struct literals with pointer fields:

```go
user := User{
    Name:  ptr.To("Alice"),
    Email: ptr.To("alice@example.com"),
    Age:   ptr.To(30),
}
```

### From — dereference safely

Returns the zero value when the pointer is nil:

```go
ptr.From[string](nil)        // ""
ptr.From(ptr.To("hello"))    // "hello"
```

### FromOr — dereference with default

```go
ptr.FromOr(nil, "fallback")          // "fallback"
ptr.FromOr(ptr.To("hello"), "nope")  // "hello"
```

## Concrete Helpers

Type-specific helpers for the most common cases:

```go
ptr.String(nil)          // ""
ptr.String(ptr.To("a")) // "a"

ptr.Int(nil)             // 0
ptr.Int(ptr.To(42))      // 42

ptr.Bool(nil)            // false
ptr.Bool(ptr.To(true))   // true
```

## Conversion Helpers

```go
ptr.IntToStr(nil)         // ""
ptr.IntToStr(ptr.To(42))  // "42"

ptr.BoolToYesNo(nil)          // ""
ptr.BoolToYesNo(ptr.To(true)) // "Yes"
ptr.BoolToYesNo(ptr.To(false))// "No"
```

## API Reference

```go
// Generic
func To[T any](v T) *T
func From[T any](p *T) T
func FromOr[T any](p *T, def T) T

// Concrete
func String(p *string) string
func Int(p *int) int
func Bool(p *bool) bool

// Conversions
func IntToStr(p *int) string
func BoolToYesNo(p *bool) string
```
