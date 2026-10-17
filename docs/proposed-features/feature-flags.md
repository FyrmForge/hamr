# Feature flags

Status: implemented (unreleased).

Deviations from the plan below:

- An enabled bool flag with no stored value reads as true. Empty or null
  values on other types serve the code default.
- The admin UI is a list page (`/admin/flags`: rule badge, served value,
  one-click Serve true/false (bool) or Enable/Disable rule via
  `POST /admin/flags/:key/toggle`, Edit link) plus a per-flag
  edit page (`/admin/flags/:key`), not one long form.
- The edit page has an "Audience" radio: Everyone saves Percent 100, Only
  listed users & roles saves 0 (needs a subject or role), Percentage of users
  plus listed uses the percent field. Bool flags use an On/Off select, so Off
  saves `false`. The dev action's empty percent is 0 when subjects or roles are
  given, else 100.
- `WithSQLite` borrows the connection per tick when the pool has one
  connection, instead of pinning it.
- The trigger SQL reaches the migrations through
  `ProjectConfig.FlagsTriggerSQL()`.
- The CLI flag is `--feature-flags`.

## Problem

hamr apps have no way to ship code dark and turn it on gradually. Env bools
via `pkg/config` work as kill switches but need a restart and can't target
users or roll out by percentage.

## Use case

Safe rollout: merge behind a flag, turn it on for 10% of users, watch errors
and logs, go to 50% / 100% or flip it off. Not experimentation: hamr picks
values, it never measures outcomes or declares a winner.

## Shape

Same split as auth (`pkg/auth` + scaffold):

- **`pkg/flags`** (library): in-memory cache, per-user evaluation, % rollout,
  typed values, change listeners, log stamping. Talks to storage through a
  small `Store` interface, like `auth.SessionStore` in `pkg/auth/session.go`.
- **Scaffold** (`hamr new`): flags table migration (pg + sqlite), the `Store`
  implementation in `internal/repo/` (sqlx + gorm variants), wiring in
  `main`. The app owns the table.
- **Existing projects**: wire it up by hand from the guide and `llms.txt`.

## Decisions

1. **No outside dependencies.** Storage is the app's own DB (Postgres or
   SQLite). No OpenFeature, no hosted vendor.
2. **Value types:** bool, int, string, JSON from day one.
3. **Sticky rollout.** `hash(flagKey + "\x00" + subjectID) % 100` picks the
   bucket, so a user keeps the same value with no stored assignment. The
   separator stops `"ab"+"c"` and `"a"+"bc"` landing in the same bucket.
4. **Reads never touch the DB.** All flags load into memory at startup.
5. **Push updates, poll as fallback.**
   - Writes through `pkg/flags` update the local cache immediately.
   - Postgres: a trigger in the scaffold migration runs `pg_notify` on every
     insert/update/delete of the flags table, so hand-written SQL notifies
     too. Every instance `LISTEN`s and reloads. After any reconnect, full
     reload (notifies sent while disconnected are lost).
   - SQLite: no cross-process subscribe (`update_hook` is per connection).
     Poll `PRAGMA data_version` about every second; reload only when it
     changes. Catches writes from other processes on the same file.
   - Slow full reload every few minutes as a safety net, both DBs.
6. **Listeners live in `pkg/flags`**, not in generated code, so apps get
   fixes on upgrade.
   - Pattern: `fraedi/internal/hubbus/hubbus.go` (dedicated connection,
     `pgx` `WaitForNotification`, reconnect loop), plus the reload-on-reconnect
     from decision 5.
   - `LISTEN` breaks silently through PgBouncer in transaction mode
     (`db.WithPgBouncerSafe` setups). The pg listener takes an optional
     direct DB URL, defaulting to `DATABASE_URL`. If it can't hold a listen
     connection it logs a warning and runs on the poll.
7. **Log stamping.** The middleware evaluates every registered flag once per
   request and puts the values on the request's context logger
   (`pkg/logging`) as a `flags` group, so every log line of that request
   traces back to a flag value. This is
   also the hook for sagr (FyrmForge observability idea), which can split
   funnels by these attributes. No metrics or comparisons in hamr.
8. **Targeting:** subject ID allow-list, role (via
   `pkg/middleware/subject.go` / `rbac.go`), and %. Nothing else.
9. **Opt-in at `hamr new`.** New `IncludeFlags` option in
   `internal/cli/generator/project.go`; off by default.

## API sketch

Flags are package-level vars in the app (no generator, no JS output). The var
holds only key + default; the live cache and the user travel on
`context.Context`, so service/repo code with a plain `ctx` can read flags.

```go
// app: internal/flags/flags.go
// hflags = github.com/FyrmForge/hamr/pkg/flags (aliased: the app's own
// package is also named flags)
var (
    NewCheckout = hflags.Bool("new_checkout", false)
    MaxUploads  = hflags.Int("max_uploads", 5)
    Theme       = hflags.JSON("theme", ThemeCfg{})
)

if flags.NewCheckout.Get(ctx) { ... }
```

```go
// pkg/flags
type Flag[T any] struct{ key string; def T }
func Bool(key string, def bool) *Flag[bool] // also Int, String, JSON[T]
func (f *Flag[T]) Get(ctx context.Context) T

func New(store Store, opts ...Option) *Client
func (c *Client) Start(ctx context.Context) error // load all + start listener
func (c *Client) Middleware(who WhoFunc) echo.MiddlewareFunc
func (c *Client) For(ctx context.Context, w Who) context.Context // jobs
func (c *Client) Set(ctx context.Context, r Rule) error

type Who struct{ SubjectID string; Roles []string }
type WhoFunc func(echo.Context) Who

type Rule struct {
    Key      string
    Enabled  bool
    Value    json.RawMessage // served when the rule matches
    Subjects []string        // allow-list
    Roles    []string
    Percent  int             // 0–100
}
type Store interface {
    List(ctx context.Context) ([]Rule, error)
    Save(ctx context.Context, r Rule) error
}

func Override[T any](ctx context.Context, f *Flag[T], v T) context.Context
func NewMemStore(rules ...Rule) Store
```

- `Get` resolves: client + `Who` from ctx → rule from memory → rule value if
  enabled and the user matches (allow-list, role, or hash under `Percent`).
  Otherwise the code default. Never errors or panics; no client in ctx →
  default.
- `Get` for every type, not `On` for bools: Go can't add a method to one
  instantiation of a generic type.
- Declaring the same key twice panics at init, so collisions fail on
  startup, not in prod traffic.
- `Middleware` runs after the auth middleware so the subject ID is set.
  `WhoFunc` is app code (scaffolded): `RequireRoles` only exposes a
  yes/no `RoleChecker`, not a role list.
- **Testing:** no separate test client. The real `Client` over
  `NewMemStore` evaluates rules exactly like prod, with no DB.
  `Override` forces one value on a ctx for unit tests. E2E tests set rules
  through `Client.Set`.
- **Bad data:** rule values decode into the flag's type once, when the cache
  loads, not per read. A value that doesn't decode serves the code default
  and logs one warning per load. `Set` decodes against the registered flag
  and rejects bad values, so this only happens on direct DB writes.
- **Logged-out visitors:** hamr sessions exist only after login
  (`CreateSession` in `pkg/auth/session.go`), so there is no ID to hash.
  - Default: only ID-free rules apply (enabled at 100%, or off); partial %
    serves the code default. No cookie.
  - Opt-in `WithAnonymousCookie(name)`: random first-party ID cookie, hashed
    for logged-out visitors. Guide must say this cookie is likely not
    "strictly necessary" under ePrivacy, so the app may need consent / a
    cookie banner before setting it.

## Toggling

- **Admin page** (scaffold, only with `IncludeFlags`): lists every flag
  declared in code with its rule and served value, a one-click Serve/Enable/Disable button and
  a per-flag edit page. Behind
  `RequireRoles(..., "admin")` (`pkg/middleware/rbac.go`). Writes go
  through `Client.Set`. Works in prod and staging.
- **Dev widget:** `pkg/flags` ships a helper returning a `devactions.Action`
  ("set flag": key select, enabled, value, percent). The scaffold registers
  it, so testers flip flags from the hamr widget without an admin login.

## Build plan

Written for parallel Sonnet workers in one tree. No git operations by
workers. Workers may run `make build`; `make ai` can show other workers'
mid-edit breakage, so the green check is Phase 2's job.

### Scaffold facts the plan relies on

- No admin pages and no `RequireRoles` use in the scaffold today. The users
  table has a `role` column (`001_initial.up.sql.tmpl`).
- sqlx apps get SQL migrations (`internal/db/migrations/001_initial*.tmpl`,
  pg and sqlite, sections gated by `{{if .IncludeX}}`). gorm apps get no SQL
  migrations: `internal/db/gorm-db.go.tmpl` runs `AutoMigrate` over
  `internal/db/models.go.tmpl`.
- `internal/web/server.go.tmpl` owns the `Deps` struct and all middleware.
  `cmd/site/main.go.tmpl` builds `Deps`.
- Options are prompted in `internal/cli/cmd/new.go` (see `Stripe`,
  `WebSocket`) and copied into `ProjectConfig`.

### Decisions for the build

- Flags table goes into `001_initial` behind `{{if .IncludeFlags}}`, like
  sessions. Existing projects add their own migration (guide shows the SQL).
- `pkg/flags` exports `PostgresTriggerSQL`. sqlx pg migration inlines the
  same SQL; gorm pg runs it with `Exec` after `AutoMigrate`. A test asserts
  the migration template and the const match.
- Admin page only when auth with users tables exists
  (`IncludeAuth && AuthWithTables`); roles come from `users.role`. Without
  it: no admin page, devactions action still wired; `Who.Roles` still comes
  from the logged-in user whenever auth is on.
- SQLite watcher pins one connection (`sql.DB.Conn`) for `PRAGMA
  data_version`. The value is per connection, so pooled reads would compare
  values from different connections.
- Listeners take `*sql.DB` (sqlx `.DB`, gorm `.DB()`), like
  `fraedi/internal/hubbus`. Optional `WithDirectURL` opens its own pgx conn
  for PgBouncer setups.

### Table contract (B and C)

Table `feature_flags` (`flags.Table`), same columns on both DBs:

| Column | pg | sqlite | Rule field |
|---|---|---|---|
| `key` | `TEXT PRIMARY KEY` | `TEXT PRIMARY KEY` | `Key` |
| `enabled` | `BOOLEAN NOT NULL DEFAULT FALSE` | `INTEGER NOT NULL DEFAULT 0` | `Enabled` |
| `value` | `TEXT NOT NULL DEFAULT 'null'` | same | `Value` (raw JSON) |
| `subjects` | `TEXT NOT NULL DEFAULT '[]'` | same | `Subjects` (JSON array) |
| `roles` | `TEXT NOT NULL DEFAULT '[]'` | same | `Roles` (JSON array) |
| `percent` | `INTEGER NOT NULL DEFAULT 0` | same | `Percent` |
| `updated_at` | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` | `TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP` | — |

Postgres trigger notifies channel `hamr_feature_flags` (`flags.Channel`)
with an empty payload on insert/update/delete; listeners reload everything.
`Save` upserts by key.

### Phase 0 (lead, before workers)

Done: compiling stubs in `pkg/flags/*` (every exported name from the API
sketch plus `Definitions`, `Client.Rules`, `Flag.Key`/`Default`, `Table`,
`Channel`, `PostgresTriggerSQL`, `WithPostgres`, `WithSQLite`,
`WithDirectURL`, `WithAnonymousCookie`, `WithPollInterval`, `WithLogger`,
`DevAction`) and `IncludeFlags` on `ProjectConfig`. The stub files are the
source of truth for signatures.

### Phase 1 (parallel)

| Worker | Owns | Delivers |
|---|---|---|
| A core | `pkg/flags/flags.go`, `client.go`, `eval.go`, `middleware.go`, `memstore.go`, their tests | Registry (duplicate key panics), typed `Flag[T]`, `Get` resolution, hash with separator, allow-list/role/% matching, decode-on-load with warn-once, `Set` validation, local cache update on `Set`, slow full-reload poll, `For`, `Override`, middleware (Who into ctx, `flags` group on context logger, anonymous cookie opt-in), `NewMemStore` |
| B listeners | `pkg/flags/listen_postgres.go`, `listen_sqlite.go`, `devaction.go`, their tests | `PostgresTriggerSQL`; LISTEN loop on a dedicated conn, reconnect every 1s, full reload after reconnect, warn + poll-only when it can't listen, `WithDirectURL`; SQLite `data_version` watcher on a pinned conn, ~1s; `DevAction` (key select from registry, enabled, value, percent) calling `Client.Set`. Pg tests reuse the testcontainers pattern in `pkg/db/db_testcontainers_integration_test.go` |
| C scaffold core | `internal/cli/cmd/new.go`, `prompt_test.go`, `internal/cli/generator/project.go`, `project_test.go`, migrations, `models.go.tmpl`, `gorm-db.go.tmpl`, new `internal/repo/{postgres,sqlite,gorm}/flags.go.tmpl`, `repo.go.tmpl`, new `internal/flags/flags.go.tmpl`, `cmd/site/main.go.tmpl`, `internal/devactions/devactions.go.tmpl` | Prompt + `IncludeFlags`; table + trigger; `Store` per connector × DB; app flags file with one example flag; main builds the client, `Start`s it, starts the right watcher, sets `Deps.Flags`; registers `DevAction`. Generator tests across gorm/sqlx × pg/sqlite × auth/no-auth, each must `go build` |
| D scaffold web | `internal/web/server.go.tmpl`, new `internal/web/handler/admin/flags/` (handler + templ) | `Deps.Flags *hflags.Client`; flags middleware after `auth.Load()` with a `WhoFunc` reading subject ID and `users.role`; admin list/edit page behind `RequireRoles(..., "admin")`, only when auth with users tables |

Contract between C and D: the `Deps` field is `Flags *hflags.Client`; D adds
it, C sets it. C lists D's admin files in `project.go` (gated on
`IncludeFlags && IncludeAuth && AuthWithTables`):
`templates/new/internal/web/handler/admin/flags/handler.go.tmpl` →
`internal/web/handler/admin/flags/handler.go` and `flags.templ.tmpl` →
`flags.templ`. The app's `internal/flags` package must be imported by
`cmd/site/main.go` (blank import if unused) or its flags never register.

### Phase 2 (lead, sequential)

Integrate, `make ai`, generate each scaffold matrix cell and build it, hostile
review.

### Phase 3 (docs worker)

`docs/guide/pkg/flags.md` (incl. consent warning for the anonymous cookie,
PgBouncer note, existing-project SQL), `README.md` package table,
`docs/guide/pkg/README.md`, `llmsdocs/llms.txt`, `llmsdocs/llms-full.txt`,
`internal/cli/generator/templates/skills/hamr/references/packages.md`,
`internal/cli/generator/templates/new/root/AGENTS.md.tmpl`,
`docs/guide/cli.md` if the prompt list is documented there,
`docs/changelog.md`, this file's status line. Then repo-wide grep for
`IncludeFlags`, `pkg/flags`, `hflags` to confirm docs cover every surface.

## Related

- `pkg/websocket/pg_hub.go` is a TODO for the same `LISTEN` pattern. A
  shared pg listen helper could serve both; out of scope here.
