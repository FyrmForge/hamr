# Database

A HAMR project talks to PostgreSQL or SQLite through embedded migrations and a repository layer that services call. You pick the database and the connector (sqlx or GORM) at scaffold time.

- **PostgreSQL** (default, `--database=postgres`) — via `hamr/pkg/db`, which wraps pgx+sqlx with retry logic, connection pooling defaults, and migration helpers.
- **SQLite** (`--database=sqlite`) — via `hamr/pkg/db/sqlite`, built on the pure-Go `modernc.org/sqlite` driver. No CGO, no C toolchain, no Docker Compose for local dev.

Both packages expose the same API shape (`ConnectContext`, `Migrate`, `MigrateDown`, `MigrateSteps`, `MigrateVersion`, `MigrateForce`), so the examples below show both side by side.

The connector is a separate choice: `--db-connector=sqlx` (default) uses `pkg/db` / `pkg/db/sqlite` and SQL migration files, as shown on most of this page. `--db-connector=gorm` doesn't use `pkg/db` at all — see [GORM projects](#gorm-projects).

**Package references:** [DB (PostgreSQL)](pkg/db.md), [DB SQLite](pkg/sqlite.md)

## Connecting

```go tab="Postgres" group="Database"
import "github.com/FyrmForge/hamr/pkg/db"

ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()

database, err := db.ConnectContext(ctx, config.GetEnvOrPanic("DATABASE_URL"))
if err != nil {
    log.Fatal(err)
}
defer database.Close()
```

```go tab="SQLite" group="Database"
import "github.com/FyrmForge/hamr/pkg/db/sqlite"

ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()

database, err := sqlite.ConnectContext(ctx, config.GetEnvOrDefault("DATABASE_PATH", "./data/app.db"))
if err != nil {
    log.Fatal(err)
}
defer database.Close()
```

Both return a `*sqlx.DB`. The PostgreSQL `ConnectContext` retries with exponential backoff (waits progressively longer between attempts) and validates connectivity with `PingContext` on each retry. The SQLite one creates the parent directory if it's missing. PostgreSQL projects read `DATABASE_URL`; SQLite projects read `DATABASE_PATH`.

> [!NOTE]
> When `hamr dev` walks the Postgres container's host port (because 5432 was busy on your machine), `DATABASE_URL` is auto-rewritten in spawned children's env — your code keeps reading `DATABASE_URL` literally and the value points at the walked port. `make migrate`, `./scripts/db-shell.sh`, and any other Makefile target prefixed with `$(ENV_LOAD)` get the same rewrite via `hamr env`. See [Port walks](hamr-dev.md#port-walks) for the mechanism.

### PostgreSQL connection options

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

### PgBouncer compatibility

[PgBouncer](https://www.pgbouncer.org/) is a connection pooler often used in production to limit direct connections to PostgreSQL. If you're not using PgBouncer, skip this section.

When routing through PgBouncer transaction pooling, enable safe mode:

```go
database, err := db.ConnectContext(ctx, databaseURL,
    db.WithPgBouncerSafe(true),
)
```

This switches pgx to simple protocol mode, avoiding prepared-statement conflicts that arise because PgBouncer may route queries to different server connections.

### SQLite connection defaults

`sqlite.ConnectContext` applies safe defaults on every pooled connection:

- `journal_mode=WAL` — concurrent readers, better performance
- `foreign_keys=ON` — SQLite disables these by default
- `busy_timeout=5000` — 5s wait on lock contention instead of immediate failure
- `temp_store=MEMORY` and `journal_size_limit=67108864` (64 MB)
- `_txlock=immediate` — transactions take the write lock at `BEGIN`
- `PRAGMA optimize=0x10002` runs once after connecting

Override via functional options: `WithJournalMode`, `WithForeignKeys`, `WithBusyTimeout`, `WithMaxOpenConns`, `WithTxLock`, and `WithPragma(name, value)` for any other pragma (e.g. `cache_size`, `synchronous`). See [pkg/db/sqlite](pkg/sqlite.md).

## Migrations

Embed SQL migration files and run them with [golang-migrate](https://github.com/golang-migrate/migrate). `//go:embed` can't reach outside its package directory, so the scaffold embeds the files in `internal/db/db.go` and exposes the config:

```go tab="Postgres" group="Database" title="internal/db/db.go"
//go:embed migrations/*.sql
var migrations embed.FS

func MigrateConfig() db.MigrateConfig {
    return db.MigrateConfig{
        FS:        migrations,
        Directory: "migrations",
    }
}
```

```go tab="SQLite" group="Database" title="internal/db/db.go"
//go:embed migrations/*.sql
var migrations embed.FS

func MigrateConfig() sqlite.MigrateConfig {
    return sqlite.MigrateConfig{
        FS:        migrations,
        Directory: "migrations",
    }
}
```

Run them with the matching package:

```go tab="Postgres" group="Database"
err := db.Migrate(database, appdb.MigrateConfig()) // appdb = <module>/internal/db
```

```go tab="SQLite" group="Database"
err := sqlite.Migrate(database, appdb.MigrateConfig()) // appdb = <module>/internal/db
```

`ErrNoChange` is ignored, so rerunning migrations is always safe. SQLite uses golang-migrate's pure-Go `database/sqlite` driver.

### Migration files

Name files with sequential numbers (`make migrate-create NAME=add_posts` writes the next pair):

```text
internal/db/migrations/
├── 001_create_users.up.sql
├── 001_create_users.down.sql
├── 002_create_sessions.up.sql
└── 002_create_sessions.down.sql
```

### Separate migration entrypoint

Keep migrations out of your HTTP server startup. The scaffold ships `cmd/migrate` (`up`, `down [N]`, `status`, `create <name>`, `force <version>`) behind Makefile targets:

```sh
make migrate                         # go run ./cmd/migrate up
make migrate-down                    # roll back the last migration
make migrate-status                  # current version (and dirty flag)
make migrate-create NAME=add_posts   # new up/down pair in internal/db/migrations/
```

Its `up` is just:

```go title="cmd/migrate/main.go"
database, err := db.ConnectContext(ctx, envDatabaseURL)
// ...
if err := db.Migrate(database, appdb.MigrateConfig()); err != nil {
    fatal("migration failed: %v", err)
}
```

Scaffolding with `--migrate-startup` drops `cmd/migrate` and these targets; the site runs `Migrate` on boot instead. For running migrations in production, see [Deployment](deployment.md#database-migrations).

### Rolling back

Roll back a fixed number of steps with `MigrateSteps` and a negative count — this is what `make migrate-down` does:

```go
err := db.MigrateSteps(database, appdb.MigrateConfig(), -1) // undo the last migration
```

> [!CAUTION]
> `db.MigrateDown` (and `sqlite.MigrateDown`) rolls back **every** migration, leaving an empty schema. Use it only for teardown (tests, wiping a dev database), never as "undo the last one".

### SQLite dialect differences

When writing migrations or repo queries for SQLite projects:

- Placeholders are `?`, not `$1`, `$2`.
- Use `INTEGER PRIMARY KEY` (not `SERIAL`) or `TEXT PRIMARY KEY` for IDs.
- Use `DATETIME` instead of `TIMESTAMPTZ`; defaults use `CURRENT_TIMESTAMP`.
- Booleans are `INTEGER` (0/1) — scaffolded `active` columns default to `1`.
- No `BEGIN/COMMIT` wrappers in migration files — golang-migrate handles transactions per step.

## Repository pattern

HAMR projects expose data access through a single `repo.Store` interface, split into one sub-store per domain entity and reached through an accessor:

```go
store.Users().GetByID(ctx, id)
```

The interfaces live in `internal/repo/`, beside their models:

```go title="internal/repo/repo.go"
type Store interface {
    Health(ctx context.Context) error
    auth.SessionStore // session persistence for pkg/auth
    Users() UserStore
}
```

```go title="internal/repo/user.go"
type UserStore interface {
    GetByID(ctx context.Context, id string) (*User, error)
    GetByEmail(ctx context.Context, email string) (*User, error)
    Create(ctx context.Context, user *User) error
}
```

The implementation lives in the database package (`internal/repo/postgres/` or `internal/repo/sqlite/`) — an accessor returning an unexported sub-store that holds the connection:

```go title="internal/repo/postgres/users.go"
func (s *Store) Users() repo.UserStore { return &userStore{db: s.db} }

type userStore struct {
    db *sqlx.DB
}

func (s *userStore) GetByID(ctx context.Context, id string) (*repo.User, error) {
    var u repo.User
    err := s.db.GetContext(ctx, &u,
        `SELECT id, email, password_hash, name, role, active, created_at, updated_at
         FROM users WHERE id = $1`, id)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, nil
        }
        return nil, err
    }
    return &u, nil
}
```

Method names drop the entity prefix — `Users().GetByID`, not `GetUserByID`. A missing row is `nil, nil`, not an error. Adding an entity means three things: a `FooStore` interface beside its model, a `Foo()` accessor on `Store`, and a `fooStore` implementation in each database package.

The `Store` goes into `internal/service/`, **not** into handlers. Handlers get services from the `Deps` struct in `internal/web/server.go`. `Deps` does carry the `Store`, but only the route wiring in `server.go` (e.g. the auth subject loader) and health probes use it directly — see [Routing and handlers](routing.md).

### Models and struct tags

Models live in `internal/repo/`, beside the interfaces that return them. Their struct tags depend on the connector you scaffolded with. With sqlx, `db:` tags map columns onto fields for `GetContext`/`SelectContext`, and the schema itself is the SQL in `internal/db/migrations/`. With GORM, the `gorm:` tags *are* the schema.

```go tab="sqlx" group="Connector" title="internal/repo/user.go"
type User struct {
    ID    string `db:"id"`
    Email string `db:"email"`
    // ...
}
```

```go tab="GORM" group="Connector" title="internal/repo/user.go"
type User struct {
    ID    string `gorm:"primaryKey"`
    Email string `gorm:"uniqueIndex;not null"`
    // ...
}
```

GORM projects register the structs for auto-migration in `internal/db/models.go`, which declares nothing itself:

```go title="internal/db/models.go"
func models() []any {
    return []any{&repo.Session{}, &repo.User{}}
}
```

One struct per table, used for both migration and queries — there is no separate model layer to keep in sync.

> [!WARNING]
> Don't put a GORM `default:` tag on a field whose zero value is meaningful. GORM omits a field from the INSERT when its value is the Go zero value *and* it carries a `default:` tag, so the database default fires instead. On a `bool`, that makes `false` unreachable: `Create(&repo.User{Active: false})` would store `true`. This is why the scaffolded `User.Active` has `not null` but no default, and why `internal/service/auth.go` sets `Active: true` explicitly. A `default:` on `Role` is fine — an empty role is never a value you'd want stored.

## Keep-alive (optional)

For long-lived PostgreSQL connections that may go idle, enable background pings:

```go
db.StartKeepAliveWithConfig(ctx, database, db.KeepAliveConfig{
    Interval: 30 * time.Second,
    Timeout:  3 * time.Second,
})
```

## SQLite project layout

`hamr new --database=sqlite` skips the `docker/` directory when storage is also local (nothing needs orchestration). The database file is created at `./data/<project-slug>.db` (default `DATABASE_PATH`) when the app or `make migrate` first connects; `data/` is added to `.gitignore`. The Dockerfile declares `/data` as a volume for persistence in containerised deployments — see [Deployment](deployment.md#sqlite-deployments) for volumes, backups and the single-replica limit.

## GORM projects

`hamr new --db-connector=gorm` (with either database) doesn't use `pkg/db`. The project owns `internal/db/db.go` instead:

- `ConnectContext(ctx, dsn)` opens a `*gorm.DB` with the same retry and backoff behaviour.
- `AutoMigrate(db)` migrates every struct returned by `models()` in `internal/db/models.go` — the `gorm:` tags on the models are the schema (see [Models and struct tags](#models-and-struct-tags)).

There are no SQL migration files. `cmd/migrate` has only `up` (runs `AutoMigrate`) and the Makefile only `make migrate`; there's no `migrate-down`, `migrate-status`, or `migrate-create`. With `--migrate-startup`, the site calls `AutoMigrate` on boot and `cmd/migrate` is omitted.
