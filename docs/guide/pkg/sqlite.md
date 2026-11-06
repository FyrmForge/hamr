# DB SQLite — Connection & Migrations

`hamr/pkg/db/sqlite` provides SQLite connection management and schema migration
utilities for hamr-scaffolded projects. It uses the pure-Go
`modernc.org/sqlite` driver so projects compile without CGO.

The package mirrors [`pkg/db`](db.md)'s API shape (`ConnectContext`, `Migrate`,
…) with simpler semantics appropriate for a local-file database: no retries, no
pool tuning, sensible pragmas on by default.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/db/sqlite"
```

## Connecting

```go
database, err := sqlite.Connect("./data/app.db")
```

`Connect` uses `context.Background()`. For startup cancellation/deadlines, use
`ConnectContext`:

```go
ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()

database, err := sqlite.ConnectContext(ctx, "./data/app.db")
```

`ConnectContext` creates the parent directory if missing and validates
connectivity with `PingContext`.

## Pragmas & Options

On every pooled connection, the following pragmas are applied by default:

| Pragma | Default | Option |
|--------|---------|--------|
| `journal_mode` | `WAL` | `WithJournalMode(string)` |
| `foreign_keys` | `ON` | `WithForeignKeys(bool)` |
| `busy_timeout` | `5000` ms | `WithBusyTimeout(time.Duration)` |
| `temp_store` | `MEMORY` | `WithPragma("temp_store", ...)` |
| `journal_size_limit` | `67108864` (64 MB) | `WithPragma("journal_size_limit", ...)` |

Transactions use `_txlock=immediate` by default: the write lock is taken at
`BEGIN`, so two transactions that both end up writing queue on `busy_timeout`
instead of failing instantly with `SQLITE_BUSY` (relevant once `MaxOpenConns >
1`). Change it with `WithTxLock("deferred" | "immediate" | "exclusive")`; an
empty string omits the parameter (driver default, deferred).

`WithPragma(name, value)` sets any other pragma on every pooled connection. If
the name is one hamr already sets (`journal_mode`, `foreign_keys`,
`busy_timeout`, `temp_store`, `journal_size_limit`) your value replaces hamr's;
a later call for the same name wins. The driver runs pragmas in its own order
(`busy_timeout` first, then alphabetical), so order-sensitive pragmas such as
`page_size` will not take effect on a new WAL database. SQLite silently ignores
unknown pragma names and invalid values; check with `PRAGMA name` if unsure.

```go
sqlite.WithPragma("cache_size", "-20000")   // negative = KiB (~20 MB)
sqlite.WithPragma("synchronous", "NORMAL")  // faster; in WAL the last commits can be
                                            // lost on power loss, but the database
                                            // cannot be corrupted
```

After the ping, `ConnectContext` runs `PRAGMA optimize=0x10002` once (not per
pooled connection); errors are ignored, since read-only databases reject it.
Long-running apps can run `PRAGMA optimize` periodically
themselves.

Pool sizing:

| Option | Default | Description |
|--------|---------|-------------|
| `WithMaxOpenConns` | 1 | Connection pool cap. SQLite is single-writer; raising this only helps read-heavy workloads on WAL. |

```go
database, err := sqlite.ConnectContext(ctx, "./data/app.db",
    sqlite.WithJournalMode("WAL"),
    sqlite.WithForeignKeys(true),
    sqlite.WithBusyTimeout(10*time.Second),
    sqlite.WithMaxOpenConns(4),
)
```

Pass `:memory:` as the path for an in-memory database (handy for tests).

You can also skip `ConnectContext` entirely: open the database yourself with
`sqlx.Open("sqlite", dsn)` and still pass the `*sqlx.DB` to `sqlite.Migrate`.

## Migrations

Run migrations from an `embed.FS` using `golang-migrate`. Internally the package
wraps `migrate/v4/database/sqlite` (the pure-Go driver, distinct from
`database/sqlite3` which requires CGO).

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

err := sqlite.Migrate(database, sqlite.MigrateConfig{
    FS:        migrationsFS,
    Directory: "migrations",
})
```

`ErrNoChange` is ignored, so rerunning a dedicated migration command is safe.

```go
err := sqlite.MigrateDown(database, cfg)                  // roll back all
err := sqlite.MigrateSteps(database, cfg, 1)              // +n up / -n down
version, dirty, err := sqlite.MigrateVersion(database, cfg)
err := sqlite.MigrateForce(database, cfg, version)        // fix dirty state
```

## SQL Dialect Notes

- Placeholders are `?`, not `$1`/`$2`.
- Prefer `TEXT PRIMARY KEY` or `INTEGER PRIMARY KEY` — no `SERIAL`.
- Use `DATETIME` (`CURRENT_TIMESTAMP` default) instead of `TIMESTAMPTZ`/`NOW()`.
- Booleans are stored as `INTEGER` (0/1).
- Do not wrap migration files in `BEGIN/COMMIT` — golang-migrate runs each file
  in its own transaction.

## API Reference

```go
type ConnectConfig struct {
    JournalMode  string
    ForeignKeys  bool
    BusyTimeout  time.Duration
    MaxOpenConns int
    TxLock       string      // default "immediate"; "" omits _txlock
    Pragmas      [][2]string // see WithPragma
}
type ConnectOption func(*ConnectConfig)

func Connect(path string, opts ...ConnectOption) (*sqlx.DB, error)
func ConnectContext(ctx context.Context, path string, opts ...ConnectOption) (*sqlx.DB, error)

func WithJournalMode(mode string) ConnectOption
func WithForeignKeys(enabled bool) ConnectOption
func WithBusyTimeout(d time.Duration) ConnectOption
func WithMaxOpenConns(n int) ConnectOption
func WithTxLock(mode string) ConnectOption
func WithPragma(name, value string) ConnectOption

type MigrateConfig struct {
    FS        embed.FS
    Directory string
}

func Migrate(db *sqlx.DB, cfg MigrateConfig) error
func MigrateDown(db *sqlx.DB, cfg MigrateConfig) error
func MigrateSteps(db *sqlx.DB, cfg MigrateConfig, n int) error
func MigrateVersion(db *sqlx.DB, cfg MigrateConfig) (version uint, dirty bool, err error)
func MigrateForce(db *sqlx.DB, cfg MigrateConfig, version int) error
```
