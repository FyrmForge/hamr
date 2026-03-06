# DB — PostgreSQL Connection & Migrations

`hamr/pkg/db` provides PostgreSQL connection management with retry, context-aware
health checks, optional PgBouncer-safe mode, optional keep-alive probes, and
schema migration utilities.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/db"
```

## Connecting

```go
database, err := db.Connect(os.Getenv("DATABASE_URL"))
```

`Connect` uses `context.Background()`. For startup cancellation/deadlines, use
`ConnectContext`:

```go
ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()

database, err := db.ConnectContext(ctx, os.Getenv("DATABASE_URL"))
```

Connectivity is validated with `PingContext` on each retry attempt.

## Connection Options

```go
database, err := db.ConnectContext(ctx, databaseURL,
    db.WithMaxOpenConns(20),
    db.WithMaxIdleConns(20),
    db.WithConnMaxIdleTime(5*time.Minute),
    db.WithConnMaxLifetime(30*time.Minute),
    db.WithMaxRetries(5),
    db.WithAttemptTimeout(3*time.Second),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithMaxOpenConns` | 10 | Maximum open connections |
| `WithMaxIdleConns` | 10 | Maximum idle connections |
| `WithConnMaxIdleTime` | 5m | Max time a connection can be idle |
| `WithConnMaxLifetime` | 30m | Max lifetime of a connection |
| `WithMaxRetries` | 5 | Connection retry attempts (must be >= 1) |
| `WithAttemptTimeout` | 3s | Timeout per connectivity check attempt |
| `WithPgBouncerSafe` | false | Use pgx simple protocol mode for PgBouncer transaction pooling |

Retries use exponential backoff with jitter.

## PgBouncer Compatibility

When routing through PgBouncer transaction pooling, enable PgBouncer-safe mode:

```go
database, err := db.ConnectContext(ctx, databaseURL,
    db.WithPgBouncerSafe(true),
)
```

This switches pgx to simple protocol mode to avoid prepared-statement behavior
that can conflict with transaction pooling.

## Optional Keep-Alive

Background ping loops are optional and should be enabled explicitly only when
needed.

```go
db.StartKeepAliveWithConfig(ctx, database, db.KeepAliveConfig{
    Interval: 30 * time.Second,
    Timeout:  3 * time.Second,
})
```

For backward compatibility, `StartKeepAlive(ctx, db, interval, poolSize)` still
exists, but `poolSize` is ignored and only one goroutine is started.

## Migrations

Run migrations from an `embed.FS` using `golang-migrate`.

### Embedding migrations

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS
```

### Running migrations

```go
err := db.Migrate(database, db.MigrateConfig{
    FS:        migrationsFS,
    Directory: "migrations",
})
```

`ErrNoChange` is ignored, so running this on startup is safe.

### Rolling back

```go
err := db.MigrateDown(database, db.MigrateConfig{
    FS:        migrationsFS,
    Directory: "migrations",
})
```

## Typical Usage

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/FyrmForge/hamr/pkg/config"
    "github.com/FyrmForge/hamr/pkg/db"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()

    database, err := db.ConnectContext(ctx, config.GetEnvOrPanic("DATABASE_URL"))
    if err != nil {
        log.Fatal(err)
    }
    defer database.Close()

    if err := db.Migrate(database, db.MigrateConfig{
        FS:        migrationsFS,
        Directory: "migrations",
    }); err != nil {
        log.Fatal(err)
    }
}
```

## API Reference

```go
type ConnectConfig struct { ... }
type ConnectOption func(*ConnectConfig)

func Connect(databaseURL string, opts ...ConnectOption) (*sqlx.DB, error)
func ConnectContext(ctx context.Context, databaseURL string, opts ...ConnectOption) (*sqlx.DB, error)

func WithMaxOpenConns(n int) ConnectOption
func WithMaxIdleConns(n int) ConnectOption
func WithConnMaxIdleTime(d time.Duration) ConnectOption
func WithConnMaxLifetime(d time.Duration) ConnectOption
func WithMaxRetries(n int) ConnectOption
func WithAttemptTimeout(d time.Duration) ConnectOption
func WithPgBouncerSafe(enabled bool) ConnectOption

type KeepAliveConfig struct {
    Interval time.Duration
    Timeout  time.Duration
}
func StartKeepAlive(ctx context.Context, db *sqlx.DB, interval time.Duration, poolSize int)
func StartKeepAliveWithConfig(ctx context.Context, db *sqlx.DB, cfg KeepAliveConfig)

type MigrateConfig struct {
    FS        embed.FS
    Directory string
    Driver    string
}
func Migrate(db *sqlx.DB, cfg MigrateConfig) error
func MigrateDown(db *sqlx.DB, cfg MigrateConfig) error
```

## Integration Testing (Testcontainers)

Run the DB reconnection integration test (requires Docker):

```bash
go test -mod=mod -tags=integration -count=1 ./pkg/db -run TestConnectContext_ReconnectsAfterBackendTermination
```
