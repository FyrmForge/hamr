# Background jobs

Work like cleaning expired sessions doesn't belong in a request handler. HAMR runs it in-process on cron schedules with `pkg/janitor`, and `pkg/async` covers concurrent work inside a request.

**Package references:** [Janitor](pkg/janitor.md), [Async](pkg/async.md)

For background work that should live in its own process (own deploy cadence, own resource budget) rather than inside the site binary, scaffold a separate worker binary with [`hamr add service --type worker`](cli.md#hamr-add-service).

## Janitor scheduler

Janitor runs background tasks on cron schedules with per-task timeouts (default 30s) and panic recovery (if a task panics, the scheduler catches it instead of crashing the process). A task that is still running when its next tick fires is skipped, not stacked.

The scaffold does **not** wire a janitor, so nothing deletes expired rows from the `sessions` table out of the box. An expired session is deleted when its cookie is next presented, but sessions whose cookies never come back accumulate. The example below is the cleanup task to add (run `go mod tidy` afterwards — the scaffold's `go.sum` doesn't include the janitor's cron dependency).

### Defining a task

Implement the `Task` interface:

```go
type Task interface {
    Name() string
    Run(ctx context.Context) (int64, error)
}
```

`Run` returns the number of affected items and any error. The context carries the per-task timeout.

```go title="internal/jobs/sessions.go"
type SessionCleanup struct {
    DB *sqlx.DB
}

func (t *SessionCleanup) Name() string { return "session_cleanup" }

func (t *SessionCleanup) Run(ctx context.Context) (int64, error) {
    // Bind the cutoff as a parameter: works on SQLite and Postgres alike
    // (Rebind turns ? into $1 for Postgres). Don't use NOW() — it doesn't
    // exist in SQLite, and SQLite's CURRENT_TIMESTAMP is formatted
    // differently from how Go writes expires_at.
    result, err := t.DB.ExecContext(ctx,
        t.DB.Rebind("DELETE FROM sessions WHERE expires_at < ?"), time.Now())
    if err != nil {
        return 0, err
    }
    return result.RowsAffected()
}
```

GORM projects get a `*gorm.DB`; use `db.WithContext(ctx).Exec("DELETE FROM sessions WHERE expires_at < ?", time.Now())` and return `RowsAffected` from the result.

### Running the scheduler

Wire the janitor in `cmd/site/main.go` after the database connects and before `srv.Start()`:

```go title="cmd/site/main.go"
// After db.ConnectContext(...) and store setup.
j := janitor.New(
    janitor.WithTimeout(30*time.Second),
    janitor.WithLogger(log),
).
    AddTask("@every 1h", &jobs.SessionCleanup{DB: database})

if err := j.Start(context.Background()); err != nil {
    log.Error("janitor start failed", "error", err)
    os.Exit(1)
}

// ... route registration ...

log.Info("starting server", "port", envPort, "devMode", envDevMode)
srvErr := srv.Start() // blocks until SIGINT/SIGTERM, then drains HTTP requests

// Shutdown order: HTTP is drained, now stop jobs, then close the DB they use.
j.Stop() // waits for in-flight tasks to return
_ = database.Close()

if srvErr != nil {
    log.Error("server stopped", "error", srvErr)
    os.Exit(1)
}
```

Each task gets its own cron schedule. `AddTask` returns the Janitor for chaining. `Stop` cancels the task context and blocks until running tasks return, so it's safe to close shared resources afterwards. Explicit calls rather than `defer`: the scaffold's error path exits through `os.Exit`, which skips deferred calls.

> [!WARNING]
> The janitor runs in-process, so **every replica runs every task**. Tasks must be idempotent (the `DELETE` above is). For jobs that must run once cluster-wide, gate them with a `WithPreTick` hook (e.g. a Postgres advisory lock) or move them to a separate worker.

### Immediate execution

Add `WithRunImmediately()` to fire every task once at startup — useful for
clearing stale data without waiting for the first cron tick:

```go
j := janitor.New(
    janitor.WithTimeout(30*time.Second),
    janitor.WithLogger(log),
    janitor.WithRunImmediately(),
).
    AddTask("@every 1h", &jobs.SessionCleanup{DB: database})
```

### Schedule expressions

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
    // Per-task: receives the task name.
    janitor.WithPreRun(func(ctx context.Context, taskName string) error {
        log.Info("task starting", "task", taskName)
        return nil // return an error to skip the task
    }),
    janitor.WithPostRun(func(ctx context.Context, taskName string, affected int64, taskErr error) {
        log.Info("task done", "task", taskName, "affected", affected, "error", taskErr)
    }),

    // Per-execution, before the per-task hooks: no task name. Use for
    // cross-cutting gates such as leader election or a maintenance flag.
    janitor.WithPreTick(func(ctx context.Context) error {
        if !isLeader(ctx) {
            return errors.New("not leader") // skips this execution
        }
        return nil
    }),
    janitor.WithPostTick(func(ctx context.Context) {
        // e.g. release a lock, flush metrics
    }),
)
```

Each option can be passed multiple times; hooks run in order and the first error skips the run.

## Async helpers

The `async` package provides concurrent execution primitives for fan-out inside a request. Following the [layering rule](structure.md#layers-and-conventions), the store calls below live in a service method (`s.store`), not in the handler.

### All: first error cancels

Run N functions concurrently. First error cancels remaining work:

```go
var user *repo.User
var orders []Order

err := async.All(ctx,
    func(ctx context.Context) error {
        var err error
        user, err = s.store.Users().GetByID(ctx, id)
        return err
    },
    func(ctx context.Context) error {
        var err error
        orders, err = s.store.Orders().ListByUser(ctx, id)
        return err
    },
)
```

### Settle: never short-circuits

Run N functions concurrently and collect all errors. Each closure writes only its own result variable and declares its own `err`:

```go
var profile *Profile
var prefs *Preferences

errs := async.Settle(ctx,
    func(ctx context.Context) error {
        var err error
        profile, err = s.store.Profiles().GetByUserID(ctx, id)
        return err
    },
    func(ctx context.Context) error {
        var err error
        prefs, err = s.store.Preferences().GetByUserID(ctx, id)
        return err
    },
)
// errs[i] is nil for successful slots; a nil profile with errs[0] != nil
// can be rendered as a partial page instead of failing the request.
```

### Map: concurrent transform

Apply a function to every item in a slice. Results keep input order; the first error cancels the rest:

```go
// userIDs []string — matches the scaffold's Users().GetByID(ctx, string) (*User, error)
users, err := async.Map(ctx, userIDs, func(ctx context.Context, id string) (*repo.User, error) {
    return s.store.Users().GetByID(ctx, id)
})
```

`Map` starts one goroutine per item with no limit — keep slices small, or use a `Group` with `WithLimit` for large fan-outs.

### Fire-and-forget

```go
// Single goroutine with panic recovery
async.Fire(func() {
    analytics.Track("page_view", props)
})

// Managed group with concurrency limiting
g := async.NewGroup(
    async.WithLimit(10),          // Go blocks while 10 are in flight
    async.WithGroupLogger(log),   // panic recovery output (default slog.Default())
    async.WithMetrics(metrics),   // optional async.GroupMetrics observer
)
for _, job := range jobs {
    g.Go(func() { process(job) })
}
g.Close() // blocks until all goroutines finish; later Go calls are no-ops
```

`GroupMetrics` callbacks (`Blocked`, `Dispatched`, `Completed`, `Panicked`) run synchronously on the hot path — they must be fast, concurrency-safe and must not panic.

For a long-lived group (e.g. a transcode pool), create it in `main.go` and call `Close()` during shutdown alongside `j.Stop()`.
