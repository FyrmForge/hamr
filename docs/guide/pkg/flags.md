# Flags — Feature Flags

`hamr/pkg/flags` ships code dark and turns it on gradually. Flags are typed
vars declared in your code. Their rules live in your own database. Reads come
from memory and never touch the DB.

This is safe rollout, not experimentation. Merge behind a flag, turn it on for
10% of users, watch errors and logs, go to 100% or flip it off. hamr picks the
value. It never measures outcomes or declares a winner.

```go
import hflags "github.com/FyrmForge/hamr/pkg/flags"
```

Alias the import as `hflags`. Your app's own package is usually called
`flags` too.

## Scaffold

`hamr new --feature-flags` (or the wizard question "Feature flags") adds:

- the `feature_flags` table in `001_initial` (Postgres also gets the notify trigger; with gorm the table is auto-migrated and the trigger runs in the same advisory-locked transaction, so replicas booting together take turns)
- a `Store` in `internal/repo/` for your connector (sqlx or gorm) and database
- `internal/flags/flags.go` with one example flag
- client wiring in `cmd/site/main.go` (`Start`, the right watcher, `Deps.Flags`)
- the flags middleware in `internal/web/server.go`, after `auth.Load()`
- a `set-flag` action in the `hamr dev` widget (`internal/devactions`)
- an admin page at `/admin/flags`, only when auth with user tables is on
- `FLAGS_DIRECT_DATABASE_URL` in `.env.example` (Postgres only)

It is off by default. For an existing project see
[Adding to an existing project](#adding-to-an-existing-project).

## Declaring flags

Declare each flag once, as a package-level var. The var holds the key and the
code default. Put them in your app's `internal/flags` package.

```go
// internal/flags/flags.go
package flags

import hflags "github.com/FyrmForge/hamr/pkg/flags"

type ThemeCfg struct {
    Accent string `json:"accent"`
}

var (
    NewCheckout = hflags.Bool("new_checkout", false)
    MaxUploads  = hflags.Int("max_uploads", 5)
    Banner      = hflags.String("banner", "")
    Theme       = hflags.JSON("theme", ThemeCfg{})
)
```

Declaring the same key twice panics at init, so a collision fails on startup.
Keys must be lowercase letters, digits, `_`, `.` or `-` (they appear in admin
URLs and log fields); anything else panics too.
`cmd/site/main.go` must import the package (a blank import is fine), or its
flags never register. The scaffold does this for you.

## Reading a flag

```go
if flags.NewCheckout.Get(ctx) {
    // new path
}
limit := flags.MaxUploads.Get(ctx)
```

`Get` takes any `context.Context` that carries the client. It never errors or
panics. With no client on the ctx, no rule, a disabled rule, a user who does
not match, or a stored value that does not decode, it returns the code
default.

`Key()` and `Default()` return the declaration.

## Rules

A rule is the stored state of one flag.

```go
type Rule struct {
    Key      string
    Enabled  bool
    Value    json.RawMessage // served when the rule matches
    Subjects []string        // allow-list of subject IDs
    Roles    []string
    Percent  int             // 0-100
}
```

`Get` serves `Value` when the rule is enabled and the user matches. The user
matches if any of these holds:

1. Their subject ID is in `Subjects`.
2. They have a role in `Roles`.
3. `Percent` is 100.
4. They fall inside the `Percent` bucket.

Otherwise `Get` serves the code default. A disabled rule never matches.

Rollout is sticky. `hash(flagKey + "\x00" + subjectID) % 100` picks the
bucket. The same user keeps the same value with no stored assignment, and
raising the percent only adds users.

### Value types

| Constructor | Value column holds |
|---|---|
| `Bool(key, def)` | `true` / `false` |
| `Int(key, def)` | `42` |
| `String(key, def)` | `"text"` (a JSON string) |
| `JSON[T](key, def)` | any JSON that decodes into `T`; each `Get` decodes a fresh copy, so mutating it is safe |

### Empty and null values

An empty or `null` value is not an error.

- On a bool flag it reads as `true`. Enabling a bool flag means "on", so you
  do not need to store a value.
- On int, string and JSON flags it serves the code default.

A value that does not decode into the flag's type serves the code default and
logs one warning per load. `Client.Set` rejects such values, so this only
happens on direct DB writes.

## Client setup

```go
client := hflags.New(store,
    hflags.WithLogger(log),
    hflags.WithPostgres(db),        // or WithSQLite(db)
)
if err := client.Start(ctx); err != nil {
    log.Warn("feature flags failed to start", "error", err)
}
```

`Start` loads all rules, then runs the watcher and the safety poll until `ctx`
ends. A load error is returned but the client stays usable: it serves code
defaults and keeps retrying.

| Option | What it does |
|---|---|
| `WithLogger(l)` | Logger for warnings. Default `slog.Default()` |
| `WithPollInterval(d)` | Safety-net full reload. Default 5 minutes |
| `WithPostgres(db)` | LISTEN/NOTIFY watcher on a pgx-backed `*sql.DB` |
| `WithSQLite(db)` | `PRAGMA data_version` watcher on a `*sql.DB` |
| `WithDirectURL(url)` | Postgres only: listen on its own connection to this URL |
| `WithAnonymousCookie(name)` | Percent rollout for logged-out visitors. See [Logged-out visitors](#logged-out-visitors) |

With gorm, pass `gormDB.DB()` as the `*sql.DB`. With sqlx, pass `sqlxDB.DB`.

### The Store

```go
type Store interface {
    List(ctx context.Context) ([]Rule, error)
    Save(ctx context.Context, r Rule) error
}
```

`List` returns every rule. `Save` upserts by key. The app owns the table and
the implementation.

## Middleware

```go
site.Use(auth.Load())
site.Use(client.Middleware(func(c echo.Context) hflags.Who {
    who := hflags.Who{SubjectID: hamrmw.GetSubjectID(c)}
    if u, ok := hamrmw.GetSubject(c).(*repo.User); ok && u != nil {
        who.Roles = []string{u.Role}
    }
    return who
}))
```

Run it after the auth middleware so the subject is set. `WhoFunc` is app code:
`RequireRoles` only exposes a yes/no check, not a role list, so you map the
user to roles yourself. An empty `Who` is a logged-out visitor.

The middleware puts the client and the user on the request context, so every
`Get(ctx)` below it works. The scaffold adds it to the site routes only: on
`/api` routes and in `hamr add service` services there is no client on the
ctx, so `Get` returns the code default. Use `client.For` there. It also evaluates every declared flag once and adds
the values to the request logger. See [Logging](#logging).

## Background jobs

Jobs have no request, so give them a ctx.

```go
ctx = client.For(ctx, hflags.Who{SubjectID: userID, Roles: []string{"admin"}})
if flags.NewCheckout.Get(ctx) { ... }
```

## Changing rules

```go
err := client.Set(ctx, hflags.Rule{
    Key:     "new_checkout",
    Enabled: true,
    Percent: 10,
})
```

`Set` checks the key is declared, `Percent` is 0-100 and the value decodes
into the flag's type. Each of these failures wraps `hflags.ErrInvalid`
(`errors.Is(err, hflags.ErrInvalid)`), and the message is safe to show to a
user. Any other error comes from the store and should be logged, not shown. A
valid rule is saved to the store and the local cache is updated at once. Other instances learn about the change through the watcher.

Hand-written SQL works too. On Postgres the trigger notifies every instance.

`client.Rules()` returns the current rules sorted by key.
`hflags.Definitions()` returns every declared flag (key, type, JSON default),
sorted by key. The admin page uses both.

## Testing

No separate test client. The real `Client` over `NewMemStore` evaluates rules
exactly like production, with no DB.

```go
store := hflags.NewMemStore(hflags.Rule{Key: "new_checkout", Enabled: true, Percent: 100})
c := hflags.New(store)
_ = c.Start(ctx)
ctx = c.For(ctx, hflags.Who{SubjectID: "u1"})
```

To force one value on a ctx in a unit test:

```go
ctx = hflags.Override(ctx, flags.NewCheckout, true)
```

`Override` wins over any rule. For end-to-end tests, set rules through
`Client.Set`.

## Change propagation

Writes through `Set` update the local cache at once. Other instances hear
about changes like this:

- **Postgres.** A trigger in the migration runs `pg_notify` on channel
  `hamr_feature_flags` after every insert, update, delete or truncate. Each instance
  runs `LISTEN`, and reloads all rules on every notify. After any reconnect it
  reloads everything, because notifies sent while disconnected are lost. The
  listener retries with backoff.
- **SQLite.** There is no cross-process subscribe. The watcher polls
  `PRAGMA data_version` about once a second and reloads when it changes. That
  catches writes from other processes on the same file. Staleness is up to
  one second. The value ignores the connection's own commits, which `Set`
  already applied. If the pool has only one connection (hamr's default), the
  watcher borrows it per tick instead of pinning it.
- **Both.** A full reload every 5 minutes (`WithPollInterval`) as a safety net.

### Postgres connections and PgBouncer

`WithPostgres` pins one pooled connection for `LISTEN`. The pool needs at
least two connections, or use `WithDirectURL`. On a one-connection pool it
logs an error and does not listen, so changes arrive on the 5-minute poll.

`LISTEN` breaks silently through PgBouncer in transaction mode (for example
`db.WithPgBouncerSafe` setups). Point the listener at a direct, non-pooled
URL:

```go
if u := os.Getenv("FLAGS_DIRECT_DATABASE_URL"); u != "" {
    opts = append(opts, hflags.WithDirectURL(u))
}
```

The scaffold reads `FLAGS_DIRECT_DATABASE_URL` for you. Unset, it listens on
the pool. If the listener cannot connect it logs a warning and the client
runs on the poll alone.

## Logging

The middleware adds a `flags` group to the request's context logger, with the
value every declared flag has for that request. Every log line of the request
then traces back to a flag value:

```json
{"msg":"request","flags":{"new_checkout":true,"max_uploads":5}}
```

The scaffold's request logger re-reads the context logger after later
middleware, so the request log line carries the group too. hamr does no
metrics or comparisons. Use the group to split logs or funnels in your own
tools.

## Admin page and dev widget

**Admin page.** With `--feature-flags` and auth with user tables, the scaffold
adds `/admin/flags`: a list with one row per declared flag (key, type, a Rule
badge of No rule / Disabled / Everyone / Listed only / N%, and the value the
rule's audience gets under Serving, shown as "(default)" when the code default
applies) plus a one-click button and an Edit link. For a bool flag the button
is always shown and reads "Serve <opposite of what users get now>" (the rule
value when an enabled rule exists, else the code default): a
`Bool("checkout.enabled", true)` with no rule offers "Serve false", which
creates an enabled rule for everyone with value `false`; the button then reads
"Serve true". Toggle (`POST /admin/flags/:key/toggle`) enables the rule, sets
the value and keeps the existing audience. For other types the button reads
"Enable rule" / "Disable rule" and flips only Enabled; it is hidden ("Edit to
set a value") when there is no saved value, where enabling would change
nothing. Edit
(`GET /admin/flags/:key`, saved with `POST /admin/flags/:key`) shows the full
form; success redirects to the list with a flash, an error re-renders the form
with a 422 and your input. An unknown key is a 404. The value input follows the flag's type: an On/Off select for a bool (Off saves
`false`, so a bool can be turned off while its rule stays enabled), a number
input for an int, a text input for a string (typed without quotes), and a
textarea of raw JSON for a json flag. Empty input on those saves no value,
which serves the code default. It sits behind `RequireAuth` and
`RequireRoles(..., "admin")`, and saves through `Client.Set`. Targeting is an
"Audience" radio: Everyone saves Percent 100; "Only listed users & roles" saves
Percent 0 and needs at least one subject ID or role ("List at least one user ID
or role."); "Percentage of users, plus listed" uses the percent field (0-100,
0 allowed). A flag with no rule shows Everyone and its edit form opens with
"Enabled" ticked, so saving turns it on for all; an existing rule keeps its
stored Enabled. An `ErrInvalid` error shows as
"Could not save: <reason>."; any other (store) error is logged and the page
shows "Could not save the flag. Please try again." without the raw error (a failed toggle flashes the same message). Without auth tables there is no admin page;
`Who.Roles` still comes from the logged-in user whenever auth is on.

**Dev widget.** `hflags.DevAction(client)` returns a `devactions.Action` named
`set-flag` with inputs for key, enabled, value (JSON), percent, subjects and
roles. An empty percent means 0 when subjects or roles are given (listed only),
otherwise 100. The reply names the value when one is set ("checkout.enabled:
false for 100%", "max_uploads: 10 for 25%"), else "on for N%", or "off" when
disabled. The scaffold registers it, so testers flip flags from the `hamr dev`
widget without an admin login. It calls `Client.Set`. Call it after your flags
are declared, since it reads the flag list when built.

## Logged-out visitors

hamr sessions exist only after login, so a logged-out visitor has no ID to
hash. By default only rules that need no identity apply: enabled at 100%, or
off. A partial percent serves the code default. No cookie is set.

To roll out by percent to logged-out visitors, opt in:

```go
hflags.New(store, hflags.WithAnonymousCookie("ff_id"))
```

The middleware then sets a random first-party ID cookie (HttpOnly, SameSite
Lax, one year, Secure when the request is https, including via
`X-Forwarded-Proto` behind a proxy) and hashes it for visitors with no subject ID.

**Warning: consent.** This cookie is likely not "strictly necessary" under the
ePrivacy rules, because the site works without it. Your app may need the
visitor's consent, and a cookie banner, before it sets this cookie. Check with
your legal advisor. If you cannot get consent, do not enable the option.

## Adding to an existing project

1. Create the table.

   Postgres:

   ```sql
   CREATE TABLE feature_flags (
       key         TEXT        PRIMARY KEY,
       enabled     BOOLEAN     NOT NULL DEFAULT FALSE,
       value       TEXT        NOT NULL DEFAULT 'null',
       subjects    TEXT        NOT NULL DEFAULT '[]',
       roles       TEXT        NOT NULL DEFAULT '[]',
       percent     INTEGER     NOT NULL DEFAULT 0,
       updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
   );
   ```

   SQLite:

   ```sql
   CREATE TABLE feature_flags (
       key         TEXT PRIMARY KEY,
       enabled     INTEGER NOT NULL DEFAULT 0,
       value       TEXT    NOT NULL DEFAULT 'null',
       subjects    TEXT    NOT NULL DEFAULT '[]',
       roles       TEXT    NOT NULL DEFAULT '[]',
       percent     INTEGER NOT NULL DEFAULT 0,
       updated_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
   );
   ```

2. Postgres only: add the notify trigger. `hflags.PostgresTriggerSQL` is the
   exact, idempotent, multi-statement SQL. Paste it into a migration, or run
   it once at startup:

   ```go
   if _, err := db.ExecContext(ctx, hflags.PostgresTriggerSQL); err != nil {
       return err
   }
   ```

   `hflags.Table` and `hflags.Channel` hold the table and channel names. Without
   the trigger, instances only see changes on the 5-minute poll.

3. Implement `Store` over the table. Sketch for sqlx on Postgres. The list
   columns are JSON text, and `value` is raw JSON:

   ```go
   type flagStore struct{ db *sqlx.DB }

   func (s *flagStore) List(ctx context.Context) ([]hflags.Rule, error) {
       rows, err := s.db.QueryContext(ctx,
           `SELECT key, enabled, value, subjects, roles, percent FROM feature_flags`)
       if err != nil {
           return nil, err
       }
       defer rows.Close()
       var out []hflags.Rule
       for rows.Next() {
           var r hflags.Rule
           var value, subjects, roles string
           if err := rows.Scan(&r.Key, &r.Enabled, &value, &subjects, &roles, &r.Percent); err != nil {
               return nil, err
           }
           r.Value = json.RawMessage(value)
           // Skip a row with bad lists so one bad cell doesn't fail the whole load.
           if err := errors.Join(json.Unmarshal([]byte(subjects), &r.Subjects), json.Unmarshal([]byte(roles), &r.Roles)); err != nil {
               slog.WarnContext(ctx, "flags: bad subjects/roles", "key", r.Key, "error", err)
               continue
           }
           out = append(out, r)
       }
       return out, rows.Err()
   }

   func (s *flagStore) Save(ctx context.Context, r hflags.Rule) error {
       value := string(r.Value)
       if value == "" {
           value = "null"
       }
       subjects, _ := json.Marshal(append([]string{}, r.Subjects...))
       roles, _ := json.Marshal(append([]string{}, r.Roles...))
       _, err := s.db.ExecContext(ctx,
           `INSERT INTO feature_flags (key, enabled, value, subjects, roles, percent, updated_at)
            VALUES ($1, $2, $3, $4, $5, $6, NOW())
            ON CONFLICT (key) DO UPDATE SET
              enabled = EXCLUDED.enabled, value = EXCLUDED.value,
              subjects = EXCLUDED.subjects, roles = EXCLUDED.roles,
              percent = EXCLUDED.percent, updated_at = NOW()`,
           r.Key, r.Enabled, value, string(subjects), string(roles), r.Percent)
       return err
   }
   ```

   For SQLite use `?` placeholders, `CURRENT_TIMESTAMP`, and an integer
   `enabled` (1 or 0).

4. Wire it in `main`: import your flags package, build the client with
   `WithPostgres(db)` or `WithSQLite(db)`, call `Start`, and add the
   middleware after your auth middleware. Optionally register
   `hflags.DevAction(client)` in your dev actions.
