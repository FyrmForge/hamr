# Janitor — Background Task Scheduler

`hamr/pkg/janitor` provides a periodic background task runner with per-task timeouts,
chainable API, and pre/post hooks at both per-task and per-tick levels.

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

j := janitor.New(5*time.Minute,
    janitor.WithTimeout(30*time.Second),
    janitor.WithRunImmediately(true),
    janitor.WithLogger(logger),
).
    AddTask(&SessionCleanup{db: database}).
    AddTask(&RateLimitCleanup{store: pgStore})

if err := j.Start(ctx); err != nil {
    log.Fatal(err)
}
defer j.Stop()
```

`AddTask` returns the Janitor for chaining. `Start` validates configuration, stores the
context for use in all tick/task execution, optionally runs all tasks once immediately,
then spawns the background ticker. Cancelling the context also stops the background
goroutine.

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithTimeout(d)` | 30s | Per-task context timeout |
| `WithRunImmediately(bool)` | `false` | Run all tasks once on `Start` before ticking |
| `WithLogger(l)` | `slog.Default()` | Structured logger for task execution |
| `WithPreRun(fn)` | — | Hook called before each task |
| `WithPostRun(fn)` | — | Hook called after each task |
| `WithPreTick(fn)` | — | Hook called before any tasks in a tick |
| `WithPostTick(fn)` | — | Hook called after all tasks in a tick |

## Hooks

### Per-task hooks

```go
janitor.New(5*time.Minute,
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

### Per-tick hooks

```go
janitor.New(5*time.Minute,
    janitor.WithPreTick(func(ctx context.Context) error {
        // check if maintenance window, return error to skip entire tick
        return nil
    }),
    janitor.WithPostTick(func(ctx context.Context) {
        log.Println("tick complete")
    }),
)
```

`PreTick` returning an error skips the entire tick.

## Typical Usage

```go
func main() {
    // ... setup db, server ...

    ctx := context.Background()

    j := janitor.New(5*time.Minute,
        janitor.WithTimeout(30*time.Second),
        janitor.WithRunImmediately(true),
        janitor.WithLogger(logger),
    ).
        AddTask(&SessionCleanup{db: database}).
        AddTask(&RateLimitCleanup{store: pgStore})

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
func New(interval time.Duration, opts ...Option) *Janitor
func (j *Janitor) AddTask(task Task) *Janitor
func (j *Janitor) Start(ctx context.Context) error
func (j *Janitor) Stop()

// Options
type Option func(*Janitor)
func WithTimeout(d time.Duration) Option
func WithRunImmediately(run bool) Option
func WithLogger(l *slog.Logger) Option
func WithPreRun(fn PreRunFunc) Option
func WithPostRun(fn PostRunFunc) Option
func WithPreTick(fn PreTickFunc) Option
func WithPostTick(fn PostTickFunc) Option
```
