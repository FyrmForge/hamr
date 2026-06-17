# Janitor — Cron-Based Task Scheduler

`hamr/pkg/janitor` provides a cron-based background task runner with per-task
schedules and timeouts, chainable API, and pre/post hooks. Built on top of
[robfig/cron](https://github.com/robfig/cron).

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/janitor"
```

## Defining Tasks

Implement the `Task` interface:

```go
type Task interface {
    Name() string
    Run(ctx context.Context) (int64, error)
}
```

`Run` returns the number of affected items and any error. The context carries the
per-task timeout.

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

## Creating and Running

```go
ctx := context.Background() // or use a cancellable context

j := janitor.New(
    janitor.WithTimeout(30*time.Second),
    janitor.WithLogger(logger),
).
    AddTask("@every 5m", &SessionCleanup{db: database}).
    AddTask("0 0 * * *", &RateLimitCleanup{store: pgStore})

if err := j.Start(ctx); err != nil {
    log.Fatal(err)
}
defer j.Stop()
```

Each task gets its own cron schedule. `AddTask` returns the Janitor for chaining.
`Start` validates configuration, registers tasks with the cron scheduler, and starts it.
Cancelling the context stops the scheduler.

`Stop` cancels the scheduler and **blocks until in-flight tasks return** (both
scheduled and `WithRunImmediately` runs), so it's safe to close shared resources
like the DB right after. A task never runs concurrently with itself — an
immediate run won't overlap the first scheduled tick.

## Schedule Expressions

Standard cron format and robfig/cron descriptors are supported:

| Expression | Meaning |
|-----------|---------|
| `@every 5m` | Every 5 minutes |
| `@every 1h` | Every hour |
| `@hourly` | Top of every hour |
| `@daily` | Midnight every day |
| `@weekly` | Midnight on Sunday |
| `0 0 * * *` | Midnight every day |
| `0 3 * * 1-5` | 3 AM on weekdays |
| `*/5 * * * *` | Every 5 minutes |

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithTimeout(d)` | 30s | Per-task context timeout |
| `WithLogger(l)` | `slog.Default()` | Structured logger for task execution |
| `WithPreRun(fn)` | — | Hook called before each task |
| `WithPostRun(fn)` | — | Hook called after each task |
| `WithPreTick(fn)` | — | Hook called before each scheduled execution |
| `WithPostTick(fn)` | — | Hook called after each scheduled execution |
| `WithRunImmediately()` | off | Run all tasks once as soon as `Start` is called |

## Immediate Execution

By default, tasks only run when their cron schedule fires. Use
`WithRunImmediately()` to execute every task once at startup — useful for
clearing stale data without waiting for the first scheduled tick.

```go
j := janitor.New(
    janitor.WithTimeout(30*time.Second),
    janitor.WithLogger(logger),
    janitor.WithRunImmediately(),
).
    AddTask("@every 5m", &SessionCleanup{db: database})
```

Immediate runs go through the same hooks and timeouts as scheduled runs. They
are fired as background goroutines — `Start` does not block waiting for them to
finish. Because immediate runs bypass the cron overlap guard, a fast schedule
(e.g. `@every 1s`) could briefly overlap with an in-flight immediate run.

## Hooks

### Per-task hooks

```go
janitor.New(
    janitor.WithPreRun(func(ctx context.Context, taskName string) error {
        log.Printf("starting task: %s", taskName)
        return nil // return error to skip this task
    }),
    janitor.WithPostRun(func(ctx context.Context, taskName string, affected int64, taskErr error) {
        log.Printf("task %s: affected=%d err=%v", taskName, affected, taskErr)
    }),
)
```

`PreRun` returning an error skips that task. Multiple hooks run in order.

### Per-execution hooks

```go
janitor.New(
    janitor.WithPreTick(func(ctx context.Context) error {
        // check if maintenance window, return error to skip execution
        return nil
    }),
    janitor.WithPostTick(func(ctx context.Context) {
        log.Println("execution complete")
    }),
)
```

`PreTick` returning an error skips the execution.

## Typical Usage

```go
func main() {
    // ... setup db, server ...

    ctx := context.Background()

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

    srv, _ := server.New()
    srv.Start()
}
```

## API Reference

```go
// Task interface
type Task interface {
    Name() string
    Run(ctx context.Context) (int64, error)
}

// Hook types
type PreRunFunc func(ctx context.Context, taskName string) error
type PostRunFunc func(ctx context.Context, taskName string, affected int64, taskErr error)
type PreTickFunc func(ctx context.Context) error
type PostTickFunc func(ctx context.Context)

// Janitor
func New(opts ...Option) *Janitor
func (j *Janitor) AddTask(schedule string, task Task) *Janitor
func (j *Janitor) Start(ctx context.Context) error
func (j *Janitor) Stop()

// Options
type Option func(*Janitor)
func WithTimeout(d time.Duration) Option
func WithLogger(l *slog.Logger) Option
func WithPreRun(fn PreRunFunc) Option
func WithPostRun(fn PostRunFunc) Option
func WithPreTick(fn PreTickFunc) Option
func WithPostTick(fn PostTickFunc) Option
func WithRunImmediately() Option
```
