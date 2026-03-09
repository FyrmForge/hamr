# Database

The `db` package wraps pgx+sqlx with retry logic, connection pooling defaults, and migration helpers so you don't have to configure these manually. This guide covers connecting to PostgreSQL, running migrations, and organizing data access with the repository pattern.

**Package references:** [DB](pkg/db.md)

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

## Next Steps

- [Handlers & Routing](04-handlers-routing.md) — Wire your repos into handlers
- [Authentication](07-authentication.md) — Session storage and auth middleware
