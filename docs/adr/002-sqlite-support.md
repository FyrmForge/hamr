# ADR-002: SQLite as a First-Class Database Choice

- **Status**: Accepted
- **Date**: 2026-04-15 (accepted 2026-04-20)
- **Authors**: JamesTiberiusKirk

## Context

`hamr new` currently only scaffolds projects with PostgreSQL. Many use cases — prototypes,
embedded apps, small-scale deployments, local-first tools — would benefit from SQLite: zero
infrastructure, no Docker, no connection strings. The goal is to offer SQLite as a peer choice
alongside PostgreSQL in the wizard, generating a fully tailored project with no leftover
postgres references or unused services.

## Decisions

### Pure-Go driver: `modernc.org/sqlite`

Mechanically translated from the C SQLite source via `ccgo`. Passes SQLite's own test suite.
In production since ~2021, used by Grafana Loki and other CNCF tooling. Roughly 80-90% of
CGO sqlite3 performance for typical workloads.

Rejected `github.com/mattn/go-sqlite3` — requires CGO, complicates cross-compilation, and
forces users to install a C toolchain. For a scaffold tool targeting developer ergonomics, the
CGO-free tradeoff is worth the minor performance gap.

### Separate subpackage: `pkg/db/sqlite/`

`pkg/db/` is deeply PostgreSQL-specific: it parses pgx DSNs, configures pgx connection modes
(PgBouncer simple protocol), and does exponential-backoff retry — which makes sense for a
network database but not a local file.

Rather than cramming SQLite into `pkg/db/`, a new `pkg/db/sqlite/` subpackage mirrors the
same API shape (`ConnectContext()` → `*sqlx.DB`, plus Migrate/MigrateDown/MigrateSteps/
MigrateVersion/MigrateForce) with simpler semantics:

- Open the file, ensure parent directory exists
- Set pragmas: `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`
- No retry logic, no pool tuning (SQLite is single-writer, local file)

This adds `modernc.org/sqlite` to hamr's go.mod but does NOT increase the hamr CLI binary
size — the CLI (`cmd/hamr/`) does not import `pkg/db/sqlite/`. Only generated projects that
choose SQLite compile it.

**Rejected alternative: template-only (no framework package).** Putting all connect + migrate
code in generated templates avoids the framework dependency but means every generated project
carries ~130 lines of boilerplate migration driver code that can't be unit tested at the
framework level. A framework package is testable, maintainable, and keeps generated code
simple.

### golang-migrate's pure-Go SQLite driver

golang-migrate ships two sqlite packages. The `database/sqlite3` package pulls
`github.com/mattn/go-sqlite3` (CGO) — rejected. The `database/sqlite` package,
however, imports `modernc.org/sqlite` directly and is pure-Go. We use that.

`pkg/db/sqlite/migrate.go` wraps `migrate/v4/database/sqlite.WithInstance` and
`source/iofs` to provide the same API surface as `pkg/db` (`Migrate`,
`MigrateDown`, `MigrateSteps`, `MigrateVersion`, `MigrateForce`).

The original ADR proposed a hand-rolled ~80-line `database.Driver` implementation
to avoid pulling the official driver. That was based on conflating the two
packages; once verified, reusing the official pure-Go driver is strictly better
— zero custom migration code to maintain and the driver is battle-tested via
golang-migrate's own test suite.

### GORM SQLite via `github.com/glebarez/sqlite`

The official GORM driver (`gorm.io/driver/sqlite`) uses `mattn/go-sqlite3` (CGO). The
community `github.com/glebarez/sqlite` wraps `modernc.org/sqlite` for CGO-free GORM support.
This dependency only appears in generated projects' `go.mod` when gorm + sqlite is selected —
it is not added to the hamr framework.

### Template branching on `.Database` field

Templates use `{{if eq .Database "sqlite"}}` conditionals alongside the existing
`{{if eq .DBConnector "sqlx"}}` conditionals. This creates a 2×2 matrix:

| | sqlx | GORM |
|---|---|---|
| **postgres** | existing | existing |
| **sqlite** | new | new |

For the repo layer, separate template directories (`internal/repo/postgres/` vs
`internal/repo/sqlite/`) keep the generated package name aligned with the backend. The sqlx
paths differ in placeholder syntax (`$1` vs `?`); the GORM paths are nearly identical since
GORM abstracts the dialect.

### SQLite pragmas: WAL + foreign keys + busy timeout

Default pragmas set on every connection:

- `PRAGMA journal_mode=WAL` — enables concurrent readers, better performance
- `PRAGMA foreign_keys=ON` — SQLite disables foreign keys by default
- `PRAGMA busy_timeout=5000` — wait 5s on lock contention instead of failing immediately

These are configurable via functional options (`WithJournalMode`, `WithForeignKeys`,
`WithBusyTimeout`).

### No Docker Compose for SQLite-only projects

When database is SQLite and storage backend is local (no S3), no Docker Compose file is
generated. The `[[dev.docker_compose]]` block in `hamr.toml` is omitted. Docker targets in
the Makefile are conditional. If S3 storage is selected with SQLite, Docker Compose is still
generated for the RustFS service.

### E2E testing deferred for SQLite

The E2E templates are heavily PostgreSQL-specific (testcontainers-go postgres module,
`information_schema` queries, network aliases for container-to-container DB access). Creating
SQLite-compatible E2E templates is a separate effort. For now:

- The E2E wizard question is skipped when database is SQLite
- `Validate()` forces `IncludeE2E = false` for SQLite
- This is a follow-up task, not a blocker

### Database file location: `./data/<name>.db`

SQLite database files default to `./data/<name>.db`. The `data/` directory is added to
`.gitignore`. The Dockerfile creates `/data` and declares it as a `VOLUME` for persistent
storage in containerised deployments. The env var is `DATABASE_PATH` (not `DATABASE_URL`).

## Scope

### New files
- `pkg/db/sqlite/sqlite.go` — connect with pragmas
- `pkg/db/sqlite/migrate.go` — migration wrappers over `migrate/v4/database/sqlite`
- `pkg/db/sqlite/sqlite_test.go` — unit tests
- `templates/new/internal/repo/sqlite/store.go.tmpl` — store with `?` placeholders
- `templates/new/internal/repo/sqlite/users.go.tmpl` — user queries
- `templates/new/internal/db/migrations/sqlite/001_initial.up.sql.tmpl` — SQLite dialect
- `templates/new/internal/db/migrations/sqlite/001_initial.down.sql.tmpl`

### Modified framework files
- `go.mod` — add `modernc.org/sqlite`
- `internal/cli/cmd/prompt.go` — SQLite option in wizard
- `internal/cli/cmd/new.go` — help text
- `internal/cli/generator/project.go` — validation + file routing

### Modified templates (~19 files)
All templates with postgres-specific content gain `{{if eq .Database ...}}` conditionals:
main.go, migrate CLI, db wrappers, migrations SQL, env, go.mod, Makefile, gitignore,
hamr.toml, docker-compose, db-shell script, ADR, CI workflow, Dockerfile, README, CLAUDE.md,
AGENTS.md.

### Docs
- `docs/guide/03-database.md` — SQLite section
- `llmsdocs/llms.txt` and `llmsdocs/llms-full.txt` — mention SQLite, document `pkg/db/sqlite`
