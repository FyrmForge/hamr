# Database

HAMR supports two databases out of the box:

- **PostgreSQL** (default) — via `hamr/pkg/db`, which wraps pgx+sqlx with retry logic, connection pooling defaults, and migration helpers.
- **SQLite** — via `hamr/pkg/db/sqlite`, built on the pure-Go `modernc.org/sqlite` driver. No CGO, no C toolchain, no Docker Compose for local dev.

Choose SQLite at scaffold time with `hamr new --database=sqlite`. Both backends expose the same API shape (`ConnectContext`, `Migrate`, `MigrateDown`, `MigrateSteps`, `MigrateVersion`, `MigrateForce`) so most of the patterns in this guide apply to either. The PostgreSQL examples below are the default; the [SQLite](#sqlite) section at the end calls out what differs.

**Package references:** [DB (PostgreSQL)](pkg/db.md), [DB SQLite](pkg/sqlite.md)

---

## Connecting

```go
import "github.com/FyrmForge/hamr/pkg/db"

ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()

database, err := db.ConnectContext(ctx, config.GetEnvOrPanic("DATABASE_URL"))
if err != nil {
    log.Fatal(err)
}
defer database.Close()
```

`ConnectContext` returns a `*sqlx.DB` with retry and exponential backoff (waits progressively longer between attempts). It validates connectivity with `PingContext` on each retry.

### Connection Options

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

### PgBouncer Compatibility

[PgBouncer](https://www.pgbouncer.org/) is a connection pooler often used in production to limit direct connections to PostgreSQL. If you're not using PgBouncer, skip this section.

When routing through PgBouncer transaction pooling, enable safe mode:

```go
database, err := db.ConnectContext(ctx, databaseURL,
    db.WithPgBouncerSafe(true),
)
```

This switches pgx to simple protocol mode, avoiding prepared-statement conflicts that arise because PgBouncer may route queries to different server connections.

---

## Migrations

Embed SQL migration files and run them with [golang-migrate](https://github.com/golang-migrate/migrate):

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

err := db.Migrate(database, db.MigrateConfig{
    FS:        migrationsFS,
    Directory: "migrations",
})
```

`ErrNoChange` is ignored, so rerunning migrations is always safe.

### Migration Files

Name files with sequential numbers:

```
migrations/
├── 001_create_users.up.sql
├── 001_create_users.down.sql
├── 002_create_sessions.up.sql
└── 002_create_sessions.down.sql
```

### Separate Migration Entrypoint

Keep migrations out of your HTTP server startup. Use a dedicated command:

```go
// cmd/migrate/main.go
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

### Rolling Back

```go
err := db.MigrateDown(database, db.MigrateConfig{
    FS:        migrationsFS,
    Directory: "migrations",
})
```

---

## Repository Pattern

Now that you have a database connection and migrations in place, you need a way to query data. HAMR projects organize data access using repositories — one per domain entity. Repositories accept `*sqlx.DB` and expose typed methods:

```go
type UserRepo struct {
    db *sqlx.DB
}

func NewUserRepo(db *sqlx.DB) *UserRepo {
    return &UserRepo{db: db}
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
    var user User
    err := r.db.GetContext(ctx, &user, "SELECT * FROM users WHERE id = $1", id)
    if err != nil {
        return nil, err
    }
    return &user, nil
}

func (r *UserRepo) Create(ctx context.Context, u *User) error {
    _, err := r.db.NamedExecContext(ctx,
        `INSERT INTO users (name, email, password_hash)
         VALUES (:name, :email, :password_hash)`, u)
    return err
}
```

Repositories are passed into handlers via the `Deps` struct in `internal/web/server.go`.

---

## Keep-Alive (Optional)

For long-lived connections that may go idle, enable background pings:

```go
db.StartKeepAliveWithConfig(ctx, database, db.KeepAliveConfig{
    Interval: 30 * time.Second,
    Timeout:  3 * time.Second,
})
```

---

## SQLite

For prototypes, embedded apps, and local-first tools, `hamr/pkg/db/sqlite` gives you a file-backed database with no Docker or connection strings. It uses `modernc.org/sqlite` (pure Go) so your project compiles without CGO.

### Connecting

```go
import "github.com/FyrmForge/hamr/pkg/db/sqlite"

ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()

database, err := sqlite.ConnectContext(ctx, config.GetEnvOrDefault("DATABASE_PATH", "./data/app.db"))
```

`ConnectContext` creates the parent directory if missing and applies safe defaults on every pooled connection:

- `journal_mode=WAL` — concurrent readers, better performance
- `foreign_keys=ON` — SQLite disables these by default
- `busy_timeout=5000` — 5s wait on lock contention instead of immediate failure

Override via functional options: `WithJournalMode`, `WithForeignKeys`, `WithBusyTimeout`, `WithMaxOpenConns`.

### Migrations

Same shape as PostgreSQL — embed an `embed.FS` and call `Migrate`:

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

err := sqlite.Migrate(database, sqlite.MigrateConfig{
    FS:        migrationsFS,
    Directory: "migrations",
})
```

Under the hood this wraps golang-migrate's pure-Go `database/sqlite` driver.

### SQL Dialect Differences from PostgreSQL

When writing migrations or repo queries for SQLite-flavoured projects:

- Placeholders are `?`, not `$1`, `$2`.
- Use `INTEGER PRIMARY KEY` (not `SERIAL`) or `TEXT PRIMARY KEY` for IDs.
- Use `DATETIME` instead of `TIMESTAMPTZ`; defaults use `CURRENT_TIMESTAMP`.
- Booleans are `INTEGER` (0/1) — scaffolded `active` columns default to `1`.
- No `BEGIN/COMMIT` wrappers in migration files — golang-migrate handles transactions per step.

### Scaffolded Layout

`hamr new --database=sqlite` skips the `docker/` directory when storage is also local (nothing needs orchestration). The database file is created at `./data/<name>.db` on first migration; `data/` is added to `.gitignore`. The Dockerfile declares `/data` as a volume for persistence in containerised deployments. The env var is `DATABASE_PATH`, not `DATABASE_URL`.

---

## Next Steps

- [Handlers & Routing](04-handlers-routing.md) — Wire your repos into handlers
- [Authentication](07-authentication.md) — Session storage and auth middleware
