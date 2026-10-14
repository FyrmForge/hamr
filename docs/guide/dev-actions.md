# Dev Actions

Dev actions let the hamr widget run app-defined buttons ("Wipe & reseed", "Create account", ...) and offer seeded test logins, locally under `hamr dev` and on a deployed staging site behind the [`hamr mock-serve` front door](cli.md#front-door-mode-staging).

`hamr/pkg/devactions` is the app side: one token-gated endpoint, mounted at `/_devactions`, that lists actions and test accounts and runs actions on request. New projects get it wired up in `internal/devactions` (see [Scaffold](#scaffold)).

## Quick Start

```go
import hamrdev "github.com/FyrmForge/hamr/pkg/devactions"

r := &hamrdev.Registry{
    Disposable: func(ctx context.Context) (bool, error) { return disposable(ctx, db) },
    Accounts: func(ctx context.Context) ([]hamrdev.Account, error) {
        return []hamrdev.Account{{Email: "admin@test.com", Password: "Test1234!", Label: "admin"}}, nil
    },
}
r.Register(hamrdev.Action{
    Name:  "clear-cache",
    Label: "Clear cache",
    Run: func(ctx context.Context, in map[string]string) (string, error) {
        cache.Clear()
        return "Cache cleared", nil
    },
})

if h := r.Handler(); h != nil { // nil unless the env gate passes
    e.Any(hamrdev.Prefix, echo.WrapHandler(h))
    e.Any(hamrdev.Prefix+"/*", echo.WrapHandler(h))
}
```

Mount it on the root router, not behind CSRF, session or auth middleware: it authenticates with its own token. The handler matches the full request path, so mount it at the app root.

## API

```go
const Prefix = "/_devactions"

type Registry struct {
    Accounts   func(ctx context.Context) ([]Account, error) // optional
    Disposable func(ctx context.Context) (bool, error)      // required if any action is Dangerous
    WhoAmI     func(r *http.Request) (*Account, error)      // optional: who the browser is logged in as
}
func (r *Registry) Register(a Action)   // panics on empty, duplicate or "whoami" (reserved) Name, or one that is not a single path segment ([A-Za-z0-9._-], not "." or "..")

// SessionWhoAmI builds WhoAmI from a pkg/auth session manager: it validates
// the session cookie, then lookup maps the subject ID to an Account.
func SessionWhoAmI(sm *auth.SessionManager, lookup func(ctx context.Context, subjectID string) (*Account, error)) func(*http.Request) (*Account, error)
func (r *Registry) Handler() http.Handler // nil when the env gate fails

type Action struct {
    Name, Label, Description string
    Inputs    []Input
    Dangerous bool
    Run       func(ctx context.Context, in map[string]string) (message string, err error)
}

type Input struct {
    Name, Label string
    Type        InputType // InputText, InputNumber, InputBool, InputSelect
    Options     []string  // InputSelect only
    Default     string
    Required    bool
}

type Account struct{ Email, Password, Label, Phone string } // Phone optional
```

`Handler` panics if a `Dangerous` action is registered without `Disposable`.

### Inputs

All inputs reach `Run` as strings in `in`, keyed by `Input.Name`. Empty values fall back to `Default`. hamr validates before calling `Run`: `Required` must be non-empty, `InputNumber` must parse as a number, `InputBool` must be `true` or `false`, `InputSelect` must be one of `Options`. A failure returns 400 and `Run` is not called.

`Run` returns a message shown in the widget, or an error (shown, HTTP 500).

## Env gate

`Handler()` returns nil, and the endpoint does not exist, unless both are set:

| Env | Value |
|-----|-------|
| `HAMR_ACTIONS_TOKEN` | any non-empty secret |
| `HAMR_ACTIONS_ENV` | `development` or `staging` |

Never set these in production. Every call must carry `X-Hamr-Actions-Token: <token>`; a wrong token gets 401.

- `hamr dev` generates a random per-run token and injects `HAMR_ACTIONS_TOKEN` and `HAMR_ACTIONS_ENV=development` into the app. Nothing to configure.
- On staging behind the front door, set the same `HAMR_ACTIONS_TOKEN` on `hamr mock-serve` and on the app, and `HAMR_ACTIONS_ENV=staging` on the app.

hamr calls the endpoint server-side. The widget talks to `/__hamr/app/actions`, which hamr forwards to `/_devactions` with the token. On the front door's public port `/_devactions` is always 404; `/__hamr/app/actions` and `/__hamr/app/whoami` are served behind the password gate when `HAMR_ACTIONS_TOKEN` is set (anyone with the gate password can run the actions), and the `hamr dev` tunnel blocks `/__hamr/app/`.

## HTTP shape

| Request | Result |
|---------|--------|
| `GET /_devactions` | `{"actions": [...], "accounts": [...]}` |
| `POST /_devactions/<name>` body `{"inputs": {...}, "confirm": true}` | `{"message": "..."}` |
| `GET /_devactions/whoami` (browser cookies forwarded by hamr) | `{"account": {"email", "label", "phone"}}` or `{"account": null}`; 404 without `WhoAmI`. Never includes the password |

Errors are `{"error": "..."}`: 400 bad input, 401 bad token, 404 unknown action, 405 wrong method, 409 confirmation required, 412 not disposable, 500 `Run` failed.

## Dangerous actions and the disposable marker

An action with `Dangerous: true` (wipe, reseed, ...) runs only when both hold:

1. The request has `"confirm": true` (the widget asks first), else 409.
2. `Registry.Disposable` returns true, else 412.

`Disposable` is how the app says "this database is safe to wipe". The scaffold checks for a `hamr_disposable` table, which only the dev seed creates, so a production database never has it.

> On a fresh database run `make seed` (`go run ./cmd/site --seed`) once, after migrations. It loads the test accounts and creates the marker; until then "Wipe & reseed" returns 412. Deliberately not a widget action: whatever creates the marker must not be reachable through the endpoint it protects.

## Scaffold

`hamr new` generates `internal/devactions` and mounts it from `internal/web/server.go` (via `Deps.DevActions`, nil unless the env gate passes). It registers:

| Action | Dangerous | What it does |
|--------|-----------|--------------|
| `reseed` | yes | Deletes users and sessions (as the project has them), then reloads `seed.sql` |
| `create-account` | no | Creates a user: email, name, role (`admin`/`user`), password (default `Test1234!`) |
| `logout-everyone` | no | Deletes all sessions |

Seeded accounts: `admin@test.com` and `user@test.com`, password `Test1234!`. The seed lives at `internal/devactions/seed.sql` (per DB driver). Keep the `accounts` list in `devactions.go` in sync with it.

## Widget

The hamr widget (live-reload badge) gets:

- **Actions panel.** One button per action, with a form for its inputs. Dangerous actions ask for confirmation.
- **Test accounts.** The `Accounts` list, with labels.
- **Logged in as.** With `WhoAmI` set, the panel shows the current user and "Open their mail" / "Open their texts" (texts need `Phone`), which open the inboxes filtered with `?to=` to that recipient only. hamr forwards the browser's cookies (never its own gate cookie) to the app over the private address; the app resolves its own session, so this is right after logout too.
- **Login helper.** The widget watches for a login form: a `<form>` with a password field that is not `autocomplete="new-password"` (re-checked after htmx swaps). When found, the widget shows a count badge ("3 test logins") and lists the accounts first. hamr never touches the form until you click an account; then it fills email and password and submits.

The scaffold's login and register templates set `autocomplete` attributes so detection picks the login form and skips the register form. If you build your own forms, mark new-password fields `autocomplete="new-password"`.
