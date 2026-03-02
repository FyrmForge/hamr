# Ctx — Type-Safe Echo Context Keys

`hamr/pkg/ctx` provides generic, type-safe context key helpers for Echo handlers.
Eliminates string-based key lookups and manual type assertions.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/ctx"
```

## Design

Standard Echo context uses `string` keys and `any` values, requiring manual type
assertions at every call site. This package uses Go generics to make context access
type-safe at compile time.

```go
// Without pkg/ctx — string keys, manual assertion
c.Set("user_id", "abc")
id, ok := c.Get("user_id").(string) // unchecked, can panic

// With pkg/ctx — type-safe
ctx.Set(c, ctx.SubjectIDKey, "abc")
id, ok := ctx.Get(c, ctx.SubjectIDKey) // id is string, ok is bool
```

## Creating Keys

```go
var TenantKey = ctx.NewKey[string]("tenant_id")
var UserKey   = ctx.NewKey[*User]("user")
var FlagsKey  = ctx.NewKey[[]string]("feature_flags")
```

The type parameter `T` locks what can be stored and retrieved.

## Setting Values

```go
ctx.Set(c, ctx.SubjectIDKey, userID)
ctx.Set(c, UserKey, &user)
```

## Getting Values

```go
id, ok := ctx.Get(c, ctx.SubjectIDKey)
if !ok {
    // not set
}
```

For values that must be present (panics if missing):

```go
user := ctx.MustGet(c, UserKey)
```

## Pre-defined Keys

The package ships with keys used by the middleware package:

| Key | Type | Set by |
|-----|------|--------|
| `SubjectIDKey` | `string` | Auth / TrustedSubject middleware |
| `SubjectKey` | `any` | Auth middleware (loaded subject) |
| `SessionKey` | `any` | Auth middleware (session) |
| `RequestIDKey` | `string` | RequestID middleware |
| `FlashKey` | `any` | Flash middleware |

## API Reference

```go
type Key[T any] struct{ ... }
func NewKey[T any](name string) Key[T]
func (k Key[T]) String() string
func Set[T any](c echo.Context, key Key[T], value T)
func Get[T any](c echo.Context, key Key[T]) (T, bool)
func MustGet[T any](c echo.Context, key Key[T]) T

// Pre-defined keys
var SubjectIDKey = NewKey[string]("subject_id")
var SubjectKey   = NewKey[any]("subject")
var SessionKey   = NewKey[any]("session")
var RequestIDKey = NewKey[string]("request_id")
var FlashKey     = NewKey[any]("flash")
```
