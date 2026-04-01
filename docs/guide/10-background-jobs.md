# Background Jobs

Some work — like cleaning expired sessions or resetting rate limits — should not run inside a request handler because it would slow down responses and couple unrelated concerns. This guide covers the Janitor scheduler for cron jobs and async helpers for concurrent handler work.

**Package references:** [Janitor](pkg/janitor.md), [Async](pkg/async.md)

---

## Janitor Scheduler

Janitor runs background tasks on cron schedules with per-task timeouts and panic recovery (if a task panics, the scheduler catches it instead of crashing the process).

### Defining a Task

Implement the `Task` interface:

```go
type Task interface {
    Name() string
    Run(ctx context.Context) (int64, error)
}
```

`Run` returns the number of affected items and any error. The context carries the per-task timeout.

```go
type SessionCleanup struct {
    db *sqlx.DB
}

func (t *SessionCleanup) Name() string { return "session_cleanup" }

func (t *SessionCleanup) Run(ctx context.Context) (int64, error) {
    result, err := t.db.ExecContext(ctx,
        "DELETE FROM sessions WHERE expires_at < NOW()")
    if err != nil {
        return 0, err
    }
    return result.RowsAffected()
}
```

### Running the Scheduler

Wire the janitor in `main.go` before starting the server:

```go
func main() {
    // ... setup db, logger ...

    j := janitor.New(
        janitor.WithTimeout(30*time.Second),
        janitor.WithLogger(logger),
    ).
        AddTask("@every 5m", &SessionCleanup{db: database}).
        AddTask("0 3 * * *", &RateLimitCleanup{store: pgStore})

    if err := j.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer j.Stop()

    srv, _ := server.New(server.WithPort(envPort))
    srv.Start()
}
```

Each task gets its own cron schedule. `AddTask` returns the Janitor for chaining.

### Immediate Execution

Add `WithRunImmediately()` to fire every task once at startup — useful for
clearing stale data without waiting for the first cron tick:

```go
j := janitor.New(
    janitor.WithTimeout(30*time.Second),
    janitor.WithLogger(logger),
    janitor.WithRunImmediately(),
).
    AddTask("@every 5m", &SessionCleanup{db: database})
```

### Schedule Expressions

Uses [robfig/cron](https://github.com/robfig/cron) syntax:

| Expression | Meaning |
|-----------|---------|
| `@every 5m` | Every 5 minutes |
| `@every 1h` | Every hour |
| `@hourly` | Top of every hour |
| `@daily` | Midnight every day |
| `0 3 * * 1-5` | 3 AM on weekdays |
| `*/5 * * * *` | Every 5 minutes |

### Hooks

```go
janitor.New(
    janitor.WithPreRun(func(ctx context.Context, taskName string) error {
        log.Printf("starting: %s", taskName)
        return nil // return error to skip task
    }),
    janitor.WithPostRun(func(ctx context.Context, taskName string, affected int64, taskErr error) {
        log.Printf("done: %s affected=%d err=%v", taskName, affected, taskErr)
    }),
)
```

---

## Async Helpers

The `async` package provides concurrent execution primitives for handler-level parallelism.

### All — First Error Cancels

Run N functions concurrently. First error cancels remaining work:

```go
var user User
var orders []Order

err := async.All(ctx,
    func(ctx context.Context) error {
        var err error
        user, err = repo.GetUser(ctx, id)
        return err
    },
    func(ctx context.Context) error {
        var err error
        orders, err = repo.GetOrders(ctx, id)
        return err
    },
)
```

### Settle — Never Short-Circuits

Run N functions concurrently and collect all errors:

```go
errs := async.Settle(ctx,
    func(ctx context.Context) error { profile, err = repo.GetProfile(ctx, id); return err },
    func(ctx context.Context) error { prefs, err = repo.GetPreferences(ctx, id); return err },
)
// errs[i] is nil for successful slots
```

### Map — Concurrent Transform

Apply a function to every item in a slice:

```go
users, err := async.Map(ctx, userIDs, func(ctx context.Context, id int64) (User, error) {
    return repo.GetUser(ctx, id)
})
```

### Fire-and-Forget

```go
// Single goroutine with panic recovery
async.Fire(func() {
    analytics.Track("page_view", props)
})

// Managed group with concurrency limiting
g := async.NewGroup(async.WithLimit(10))
for _, job := range jobs {
    g.Go(func() { process(job) })
}
g.Close() // blocks until all goroutines finish
```

---

## Next Steps

- [Real-Time](11-real-time.md) — WebSocket hub and rooms
- [Testing](12-testing.md) — Testing background job logic
