# Staging front door, dev actions, test logins

Status: implemented (unreleased). Deviations: front door requires `HAMR_MOCK_UI_PASSWORD`; the gate cookie carries its issue time so the 7-day expiry is server-enforced; bypass matching uses the cleaned path; actions in `hamr dev` are injected via `buildHamrInjectedEnv` in `devserver.go`.

Build plan below was written for parallel workers.

## Problem

Testers on a deployed dev/staging environment can't see captured emails/SMS,
can't log in as seeded accounts without knowing them, and can't reset data.
Platforms like Railway can't password-gate an environment. `hamr dev` gives
all of this locally; staging gets none of it.

## Shape

`hamr mock-serve` gains a front-door mode. With `HAMR_MOCK_UPSTREAM` set it
sits on the public domain, gates the whole site behind one password, serves
`/__hamr/*` itself and proxies everything else to the app on the private
network, injecting the hamr widget. Without it, behaviour is unchanged.

The app exposes one token-gated endpoint (`pkg/devactions`) listing actions
(wipe, reseed, create account, run job, ...) and test accounts. The widget
renders the actions, and detects login forms to offer test accounts.

`hamr dev` gets the same actions and login helper locally.

## Decisions

1. **Login helper.** Widget detects a login form (a `<form>` with a password
   input not marked `autocomplete="new-password"`, re-checked after htmx
   swaps), badges the widget ("3 test logins"), never touches the form until
   an account is clicked, then fills email + password and submits. Accounts
   come from the app's actions endpoint and carry their password.
   `HAMR_LOGIN_SELECTOR` override: not built until needed.
2. **Front-door mode.** `HAMR_MOCK_UPSTREAM=http://app:8080`. Unset → today.
   Reuses `NewProxyHandler` (`internal/devserver/proxy.go`). Upstream
   timeouts are staging-sized (60s response header), not the dev 2s.
   Two listeners, both required in front-door mode:
   - `HAMR_MOCK_UI_PORT` = public port: gate, `/__hamr/*` UI, proxy to app.
   - `HAMR_MOCK_PORT` = private port, unchanged: Stripe `/v1` `/v2` API and
     mail/SMS ingest. Never gated, never on the public domain, so `/v1/*`
     can't shadow app routes.
   Upstream set without `HAMR_MOCK_UI_PORT` is a startup error.
3. **Public URL.** `HAMR_BASE_URL` = public origin. In front-door mode it
   defaults `HAMR_STRIPE_BASE_URL`, which stays as an override.
4. **Gate.** Password is `HAMR_MOCK_UI_PASSWORD` (existing). Front-door mode
   gates every public path; plain mode gates `/__hamr/*` as today.
   - Browser: `GET /__hamr/gate` password page, `POST` sets `__hamr_gate`
     (HttpOnly, SameSite=Lax, Secure when `HAMR_BASE_URL` is https). Value is
     `HMAC-SHA256(password, "hamr-gate")`, so changing the password logs
     everyone out and there is no server state. `Max-Age` 7 days. `?next=` only accepts
     same-origin relative paths. Failed attempt sleeps 500ms.
   - Scripts: `X-Hamr-Gate: <password>`.
   - Both stripped before forwarding. App's `Authorization` untouched.
   - Unauthenticated HTML request → 303 to the gate page; others → 401.
   - `HAMR_GATE_BYPASS=/payments,/webhooks/stripe`: segment-boundary prefix
     match (`/payments` covers `/payments/x`, not `/payments-admin`).
   - Replaces basic auth (`requirePassword` in `mockserve.go`). Breaking for
     scripts that used basic auth on the UI; changelog notes it.
5. **Actions endpoint gate (in the app).** Routes registered only when
   `HAMR_ACTIONS_TOKEN` is set AND `HAMR_ACTIONS_ENV` is `development` or
   `staging`. Each request must carry `X-Hamr-Actions-Token`, compared in
   constant time. Front door refuses to proxy public requests to `/_devactions`
   (404) so the endpoint is only reachable from hamr over the private network.
6. **Disposable marker.** Actions flagged `Dangerous` refuse unless the
   app-supplied `Disposable(ctx)` check returns true. Scaffold implements it
   as "table `hamr_disposable` exists", created by the dev seed only.
7. **Dynamic actions.** App registers actions at startup; the widget renders
   whatever the list says.

Parked: microservices / multi-upstream routing, "log in as" action
(server-side session for magic-link/OAuth apps), exposing actions as MCP
tools, request-log bug reports (dropped).

## Wire contract (app ↔ hamr)

Mounted by the app at `/_devactions`. All requests carry
`X-Hamr-Actions-Token`. Bad/missing token → 401 (routes absent entirely when
the gate in decision 5 fails → 404).

`GET /_devactions` → 200

```json
{
  "actions": [
    {
      "name": "create-account",
      "label": "Create account",
      "description": "Creates a user with the given role",
      "dangerous": false,
      "inputs": [
        {"name": "email", "label": "Email", "type": "text", "required": true},
        {"name": "role", "label": "Role", "type": "select", "options": ["admin", "user"], "default": "user"}
      ]
    }
  ],
  "accounts": [
    {"email": "admin@test.com", "password": "Test1234!", "label": "admin"}
  ]
}
```

Input `type`: `text | number | bool | select`. `options` only for `select`.
`default` is always a string.

`POST /_devactions/{name}` body `{"inputs": {"email": "..."}, "confirm": true}`

- 200 `{"message": "Created bob@test.com"}`
- 400 `{"error": "..."}` bad input; 404 unknown action
- 409 `{"error": "confirmation required"}` dangerous without `confirm`
- 412 `{"error": "database is not marked disposable"}`
- 500 `{"error": "..."}` action returned an error

hamr side re-exposes this same-origin to the widget as
`GET /__hamr/app/actions` and `POST /__hamr/app/actions/{name}`
(POST behind `guardUnsafe`), adding the token. Upstream failure → 502
`{"error": "app actions unavailable"}`; actions not configured → 404.

## Go API

```go
// pkg/devactions
type InputType string // "text" | "number" | "bool" | "select"

type Input struct {
    Name, Label string
    Type        InputType
    Options     []string
    Default     string
    Required    bool
}

type Action struct {
    Name, Label, Description string
    Inputs    []Input
    Dangerous bool
    Run       func(ctx context.Context, in map[string]string) (message string, err error)
}

type Account struct{ Email, Password, Label string }

type Registry struct {
    Accounts   func(ctx context.Context) ([]Account, error) // optional
    Disposable func(ctx context.Context) (bool, error)     // required if any action is Dangerous
    // unexported: actions
}

func (r *Registry) Register(a Action)          // panics on duplicate/empty name
// Handler returns the endpoint, or nil when the env gate (decision 5) fails.
// Callers mount it only when non-nil.
func (r *Registry) Handler() http.Handler
const Prefix = "/_devactions"
```

```go
// internal/devserver
func NewGate(password string, bypass []string, secure bool, next http.Handler) http.Handler
func NewAppActions(appBaseURL, token string) *AppActions // nil-safe methods
func (a *AppActions) RegisterRoutes(mux *http.ServeMux)
```

## Build plan

Phase 0 (lead, before workers): land compiling stubs so the tree builds
throughout: `pkg/devactions` types + `Handler` returning nil, `NewGate`
pass-through, `AppActions` with no-op `RegisterRoutes`, the new
`appActions` param on `NewProxyHandler` with all call sites updated.

Phase 1 runs in parallel in one tree; each worker owns only the files listed
and replaces the stubs. No git operations. Workers may run `make build`;
`make test`/`make ai` can show other workers' mid-edit breakage, so the green
check is Phase 2's job. Worker prompts state "run job now" is out of scope.

| Worker | Owns | Delivers |
|---|---|---|
| A devactions | `pkg/devactions/**` | Registry, Handler, env + token gate, confirm/disposable rules, JSON per contract, tests |
| B gate | `internal/devserver/gate.go`, `gate_test.go` | `NewGate` per decision 4, tests (cookie, header, bypass segments, next= validation, stripping) |
| C front door | `mockserve.go`, `proxy.go`, `cmd/mock.go`, their tests | upstream mode with two listeners, `HAMR_BASE_URL`, staging timeouts, `/_devactions` public 404, SSEBroker with empty rules so the widget runs, `NewGate` replacing `requirePassword`, register `appActions` routes in `NewProxyHandler` |
| D app actions | `appactions.go`, `appactions_test.go`, `envinject.go`, `devserver.go` | `AppActions` per contract; `hamr dev` generates a per-run token, injects `HAMR_ACTIONS_TOKEN` + `HAMR_ACTIONS_ENV=development`, passes `AppActions` to `NewProxyHandler` (param exists after Phase 0) |
| E widget | `reload.js` | Actions section (form from inputs, confirm for dangerous, result toast), accounts list, login detection + badge + fill/submit; hides itself when `/__hamr/app/actions` 404s |
| F scaffold | `internal/cli/generator/**` | Full matrix gorm/sqlx × postgres/sqlite × auth/no-auth. `internal/devactions` wiring outside the CSRF group; default actions: wipe+reseed (dangerous), create account, log out everyone; accounts from seed; dev seed creating `hamr_disposable`; `autocomplete` on login/register; env.example; generator tests |

Phase 2 (sequential, lead): integrate, `make ai`, hostile review.

Phase 3: docs worker — `docs/guide/cli.md`, `docs/guide/` page for actions,
`llmsdocs/llms.txt`, `llmsdocs/llms-full.txt`, `docs/changelog.md`, this
file's status line. Then repo-wide grep for `requirePassword`, basic-auth
mentions of `HAMR_MOCK_UI_PASSWORD`.

## Seed

One dev seed per driver (postgres, sqlite), same two accounts as the e2e seed
(`admin@test.com`, `user@test.com`, `Test1234!`), plus
`CREATE TABLE hamr_disposable`. Reseed, the accounts list and e2e all read it.
No `NOW()` in the sqlite variant.

`make seed` runs `cmd/site --seed` (not `cmd/migrate`, which isn't generated
with migrate-at-startup).

"Run job now": scaffold has no job registry; app-specific, out of scope.

Next feature, parked: hamr owns seeds. A seed interface in a hamr package
that reports its accounts and creates the marker, so hamr can run, rerun or
show seeds and the accounts list stops being a hand-kept copy of the seed.
Design after studying 2-3 real projects' seeds (sportfreelancers has bulk
data, media through storage, and post-insert aggregation).
