# Async — Concurrency Helpers

`hamr/pkg/async` provides concurrent execution primitives with panic recovery and
fire-and-forget goroutine management. Zero dependencies beyond the standard library.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/async"
```

## Design

Functions passed to `All` and `Settle` return only `error`. Values are written to
pre-declared variables via closures. This avoids generics complexity and naturally
supports mixed-type concurrent work — no union structs, no discriminators, no arity
helpers needed.

## All — first error cancels

Run N functions concurrently. The first error cancels remaining work via a derived
context. Returns that first error (or nil).

```go
var user User
var session Session
var wallet Wallet

err := async.All(ctx,
    func(ctx context.Context) error {
        var err error
        user, err = repo.GetUser(ctx, 1)
        return err
    },
    func(ctx context.Context) error {
        var err error
        session, err = repo.GetSession(ctx, 1)
        return err
    },
    func(ctx context.Context) error {
        var err error
        wallet, err = repo.GetWallet(ctx, 1)
        return err
    },
)
if err != nil {
    return err
}
// user, session, and wallet are all populated
```

Each function gets a derived context — if any function errors, the remaining functions
see a cancelled ctx and can return early. Errors pass through unwrapped so `errors.Is`
and `errors.As` work as expected.

Works equally well for same-type work:

```go
var users []User
var count int

err := async.All(ctx,
    func(ctx context.Context) error {
        var err error
        users, err = repo.ListUsers(ctx)
        return err
    },
    func(ctx context.Context) error {
        var err error
        count, err = repo.CountUsers(ctx)
        return err
    },
)
```

## Settle — never short-circuits

Run N functions concurrently and collect every error. Unlike `All`, one failure does
not cancel the others — all functions run to completion.

```go
var profile Profile
var prefs Preferences
var avatar []byte

errs := async.Settle(ctx,
    func(ctx context.Context) error {
        var err error
        profile, err = repo.GetProfile(ctx, id)
        return err
    },
    func(ctx context.Context) error {
        var err error
        prefs, err = repo.GetPreferences(ctx, id)
        return err
    },
    func(ctx context.Context) error {
        var err error
        avatar, err = storage.GetAvatar(ctx, id)
        return err
    },
)

// errs[i] is nil for successful slots, non-nil for failed ones.
// All three functions ran regardless of individual failures.
for i, err := range errs {
    if err != nil {
        log.Printf("slot %d failed: %v", i, err)
    }
}
```

## Map — concurrent transform

Apply a function to every item in a slice concurrently. Returns results in input order.
First error cancels remaining work. This is the one generic function in the package —
useful when you have a homogeneous slice to transform.

```go
doubled, err := async.Map(ctx, []int{1, 2, 3}, func(ctx context.Context, n int) (int, error) {
    return n * 2, nil
})
// doubled = [2, 4, 6]
```

Real-world example — fetch multiple resources by ID:

```go
users, err := async.Map(ctx, userIDs, func(ctx context.Context, id int64) (User, error) {
    return repo.GetUser(ctx, id)
})
```

## Fire-and-Forget

### Fire — single goroutine

Spawn a single goroutine with panic recovery. Panics are logged to `slog.Default()`.

```go
async.Fire(func() {
    analytics.Track("page_view", props)
})
```

### Group — managed goroutines with optional concurrency limiting

`Group` manages a set of fire-and-forget goroutines. `Close` waits for all in-flight
work to finish. Panics are recovered and logged.

```go
g := async.NewGroup()

for _, job := range jobs {
    g.Go(func() {
        process(job)
    })
}

g.Close() // blocks until all goroutines finish
```

#### Concurrency limiting

Use `WithLimit` to cap the number of concurrent goroutines. `Go` blocks when at the
limit until a slot frees up.

```go
g := async.NewGroup(async.WithLimit(10))

for _, url := range urls {
    g.Go(func() {
        fetch(url)
    })
}

g.Close()
```

#### Custom logger

```go
g := async.NewGroup(async.WithGroupLogger(myLogger))
```

#### Behavior after Close

Calling `Go` after `Close` is a no-op — the function is silently dropped.

## Panic Recovery

Every goroutine spawned by this package recovers from panics:

- `All`, `Settle`, `Map` — panics are converted to errors with the format
  `async: panic in job N: <value>`.
- `Fire`, `Group.Go` — panics are logged via `slog`.

## Empty Inputs

All functions handle empty inputs gracefully:

| Function | Empty input returns |
|----------|-------------------|
| `All`    | `nil`             |
| `Settle` | `[]error{}`       |
| `Map`    | `[]R{}, nil`      |

## API Reference

```go
// Concurrent execution
func All(ctx context.Context, fns ...func(context.Context) error) error
func Settle(ctx context.Context, fns ...func(context.Context) error) []error
func Map[T, R any](ctx context.Context, items []T, fn func(context.Context, T) (R, error)) ([]R, error)

// Fire-and-forget
func Fire(fn func())
func NewGroup(opts ...GroupOption) *Group
func (g *Group) Go(fn func())
func (g *Group) Close()

// Options
func WithLimit(n int) GroupOption
func WithGroupLogger(l *slog.Logger) GroupOption
```
