# DB — PostgreSQL Connection & Migrations

`hamr/pkg/db` provides PostgreSQL connection management with retry, keep-alive, and
schema migration utilities. Uses `sqlx` for query helpers and `pgx` as the driver.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/db"
```

## Connecting

```go
database, err := db.Connect(os.Getenv("DATABASE_URL"))
```

Connects with retry and exponential backoff. Defaults: 10 max open, 5 max idle, 3
retries.

### Connection Pool Options

```go
database, err := db.Connect(databaseURL,
    db.WithMaxOpenConns(20),
    db.WithMaxIdleConns(10),
    db.WithConnMaxIdleTime(30*time.Second),
    db.WithConnMaxLifetime(5*time.Minute),
    db.WithMaxRetries(5),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithMaxOpenConns` | 10 | Maximum open connections |
| `WithMaxIdleConns` | 5 | Maximum idle connections |
| `WithConnMaxIdleTime` | 5s | Max time a connection can be idle |
| `WithConnMaxLifetime` | 1m | Max lifetime of a connection |
| `WithMaxRetries` | 3 | Connection retry attempts |

Retries use linear backoff: 1s, 2s, 3s, etc.

## Keep-Alive

Launch background goroutines that periodically ping the database to keep connections
warm:

```go
db.StartKeepAlive(database, 30*time.Second, 2)
```

This launches 2 goroutines, each pinging every 30 seconds. Failures are logged via
`slog.Default()`.

## Migrations

Run migrations from an `embed.FS`. Uses `golang-migrate/migrate` under the hood.

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

`ErrNoChange` is silently ignored — safe to call on every startup.

### Rolling back

```go
err := db.MigrateDown(database, db.MigrateConfig{
    FS:        migrationsFS,
    Directory: "migrations",
})
```

### Migration file naming

```
migrations/
    001_initial.up.sql
    001_initial.down.sql
    002_add_users.up.sql
    002_add_users.down.sql
```

## Typical Usage

```go
func main() {
    _ = config.LoadEnvFile()
    dbURL := config.GetEnvOrPanic("DATABASE_URL")

    database, err := db.Connect(dbURL)
    if err != nil {
        log.Fatal(err)
    }
    defer database.Close()

    db.StartKeepAlive(database, 30*time.Second, 2)

    if err := db.Migrate(database, db.MigrateConfig{
        FS:        migrationsFS,
        Directory: "migrations",
    }); err != nil {
        log.Fatal(err)
    }

    // ... use database
}
```

## API Reference

```go
// Connection
type ConnectConfig struct { ... }
type ConnectOption func(*ConnectConfig)
func Connect(databaseURL string, opts ...ConnectOption) (*sqlx.DB, error)
func WithMaxOpenConns(n int) ConnectOption
func WithMaxIdleConns(n int) ConnectOption
func WithConnMaxIdleTime(d time.Duration) ConnectOption
func WithConnMaxLifetime(d time.Duration) ConnectOption
func WithMaxRetries(n int) ConnectOption
func StartKeepAlive(db *sqlx.DB, interval time.Duration, poolSize int)

// Migrations
type MigrateConfig struct {
    FS        embed.FS
    Directory string
    Driver    string  // default: "postgres"
}
func Migrate(db *sqlx.DB, cfg MigrateConfig) error
func MigrateDown(db *sqlx.DB, cfg MigrateConfig) error
```
