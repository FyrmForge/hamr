# Testing

Services, repositories and handlers in a scaffolded app are tested as real code against a real, throwaway database rather than mocks. Browser flows such as login and form submits are covered by go-rod E2E tests.

The app is layered as handlers → services → repositories: services depend on the `repo.Store` interface, handlers take concrete services (e.g. `*service.AuthService`), and validators are pure functions.

**Package references:** [E2E](pkg/e2e.md)

## Unit and integration tests

### Running tests

Always use the Makefile:

```sh
make test   # templ generate && go test ./...
```

### Test database

For SQLite projects, use a `:memory:` database per test — no Docker, no filesystem cleanup. Migrations are embedded in `internal/db`, so reuse its `MigrateConfig()` instead of embedding them again:

```go
import (
    "github.com/FyrmForge/hamr/pkg/db/sqlite"
    "github.com/jmoiron/sqlx"

    appdb "github.com/user/myapp/internal/db"
)

func newTestDB(t *testing.T) *sqlx.DB {
    t.Helper()
    database, err := sqlite.ConnectContext(context.Background(), ":memory:")
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = database.Close() })
    if err := sqlite.Migrate(database, appdb.MigrateConfig()); err != nil {
        t.Fatal(err)
    }
    return database
}
```

Each `:memory:` connection is an independent database, and `ConnectContext` caps the pool at one connection, so parallel tests don't share state. Use a temp file (`filepath.Join(t.TempDir(), "test.db")`) only when a test exercises on-disk behaviour like WAL checkpointing or file locking.

For PostgreSQL projects, connect to the Docker Compose database (`make docker-up`) or start one per package with [testcontainers-go](https://github.com/testcontainers/testcontainers-go), then call `db.Migrate(database, appdb.MigrateConfig())` from `github.com/FyrmForge/hamr/pkg/db`.

### Service tests

Services hold the business rules, so test them against the real store:

```go
package service_test

import (
    sqliterepo "github.com/user/myapp/internal/repo/sqlite" // aliased — pkg/db/sqlite owns the bare name
    "github.com/user/myapp/internal/service"
)

func TestAuthService_RegisterRejectsDuplicateEmail(t *testing.T) {
    ctx := context.Background()
    svc := service.NewAuthService(sqliterepo.NewStore(newTestDB(t)))

    if _, err := svc.Register(ctx, "alice@example.com", "s3cret-pass", "Alice"); err != nil {
        t.Fatal(err)
    }
    _, err := svc.Register(ctx, "alice@example.com", "other-pass", "Alice 2")
    if !errors.Is(err, service.ErrEmailTaken) {
        t.Fatalf("got %v, want ErrEmailTaken", err)
    }
}
```

Repository tests look the same, calling `store.Users().Create(...)` and friends directly.

### Handler tests

Handlers are thin glue over services, templates and middleware. Test them through the real route table with `httptest`, wired the same way `cmd/site/main.go` does. `newTestDB` from above is needed here too — copy it into each test package or move it to a shared helper package such as `internal/testutil`:

```go
package web_test

import (
    "github.com/FyrmForge/hamr/pkg/auth"
    "github.com/FyrmForge/hamr/pkg/server"

    sqliterepo "github.com/user/myapp/internal/repo/sqlite"
    "github.com/user/myapp/internal/service"
    "github.com/user/myapp/internal/web"
)

func newTestServer(t *testing.T) *server.Server {
    t.Helper()
    store := sqliterepo.NewStore(newTestDB(t))

    srv, err := server.New(server.WithDevMode(true))
    if err != nil {
        t.Fatal(err)
    }
    web.RegisterRoutes(srv, &web.Deps{
        Store:          store,
        DevMode:        true,
        SessionManager: auth.NewSessionManager(store),
        AuthService:    service.NewAuthService(store),
    })
    return srv
}

func TestLoginPage_Renders(t *testing.T) {
    srv := newTestServer(t)

    req := httptest.NewRequest(http.MethodGet, "/login", nil)
    rec := httptest.NewRecorder()
    srv.Echo().ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", rec.Code)
    }
}
```

This exercises middleware, auth guards and templates together. State-changing `POST`s go through the CSRF middleware and are rejected without a valid token, so cover form flows (login, register, submit) with E2E tests instead.

### Validator tests

Validators are pure functions — easy to test:

```go
func TestEmail(t *testing.T) {
    assert.Empty(t, validate.Email("user@example.com"))
    assert.NotEmpty(t, validate.Email("not-an-email"))
    assert.NotEmpty(t, validate.Email(""))
}
```

## E2E testing

HAMR provides go-rod browser helpers for end-to-end tests.

### Setup

`hamr new --e2e` scaffolds an `e2e/` package. E2E scaffolding is **PostgreSQL-only** — the flag is ignored for SQLite projects. The `//go:build e2e` constraint keeps these tests out of `make test`.

| Target | What it does |
|--------|--------------|
| `make e2e` | Starts Postgres and the app (built from `cmd/site/Dockerfile`) in testcontainers, waits for the app to create the schema, seeds, runs all tests |
| `make e2e-local` | `make build`, then spawns `bin/site` on the first free port from 8080 against the local DB, seeds, runs, kills it. Needs `make docker-up` and `make migrate` first |
| `make e2e-run T=TestName` | Containerized, single test (`-run`) |
| `make e2e-run-local T=TestName` | Local, single test |

The containerized run expects the server container to migrate its own database, so it needs a project scaffolded with `--migrate-startup`. Local runs write server output to `e2e/testdata/e2e-artifacts/site.log`. Set `E2E_SERVER_URL` to attach to a server you're already running instead of spawning one.

The scaffold's `e2e/helpers.go` provides `openPage(t, browser, path)`, which prefixes the server URL for whichever mode is active:

```go
//go:build e2e

package e2e

import (
    "testing"

    "github.com/FyrmForge/hamr/pkg/e2e"
)

func TestDashboard(t *testing.T) {
    browser := e2e.SetupBrowser(t)
    page := openPage(t, browser, "/dashboard")

    e2e.AssertElementExists(t, page, ".welcome-banner")
    e2e.AssertElementContainsText(t, page, "h1", "Welcome")
}
```

### Configuration

```go
browser := e2e.SetupBrowser(t,
    e2e.WithHeadless(false),        // visible browser for debugging
    e2e.WithSlowMotion(500*time.Millisecond),
    e2e.WithTimeout(15*time.Second),
)
```

All options can be overridden via env vars (`E2E_HEADLESS`, `E2E_TIMEOUT`, etc.).
For headed debugging with a real GPU and a usable window: `E2E_HEADLESS=false E2E_GPU=true E2E_WINDOW_SIZE=1280x800`.
See [pkg/e2e](pkg/e2e.md#configuration) for the full option table.

### Interaction helpers

```go
e2e.Input(t, page, "#email", "alice@example.com")
e2e.Click(t, page, "button[type=submit]")
e2e.SelectOption(t, page, "#country", "US")
e2e.WaitForElement(t, page, ".toast-success", 5*time.Second)
e2e.WaitForElementRemoved(t, page, ".spinner", 5*time.Second)
e2e.WaitForURLChange(t, page, currentURL, 5*time.Second)
```

### Assertions

All assertions use `assert` (non-fatal) so multiple checks report all failures:

```go
e2e.AssertElementExists(t, page, ".welcome-banner")
e2e.AssertElementNotExists(t, page, ".deleted-item")
e2e.AssertElementNotVisible(t, page, "#modal")
e2e.AssertElementHasClass(t, page, "#email", "is-invalid")
e2e.AssertElementContainsText(t, page, "h1", "Dashboard")
e2e.AssertURL(t, page, serverURL+"/dashboard")
e2e.AssertURLContains(t, page, "/dashboard")
e2e.AssertElementCount(t, page, ".user-row", 5)
```

For custom checks, the getters return values instead of asserting:

```go
e2e.ElementExists(t, page, ".banner")         // bool
e2e.ElementText(t, page, "h1")                // string
e2e.ElementAttribute(t, page, "a.next", "href")
e2e.ElementCount(t, page, ".user-row")        // int
```

### HTMX-aware waiters

Standard page-load waits don't work with HTMX because swaps happen after the initial page load via asynchronous XHR requests. Use these helpers:

```go
e2e.Click(t, page, "#load-more")
e2e.WaitForHTMXIdle(t, page, 5*time.Second)
e2e.AssertElementExists(t, page, ".new-items")

// Or in one step:
e2e.ClickAndWaitHTMX(t, page, "#delete-item", 5*time.Second)
```

`WaitForHTMXSwap(t, page, selector, timeout)` waits until the matched element's HTML differs from what it was **when the call started**. If the swap may already have finished by then, use `WaitForHTMXIdle` or `WaitForElement` instead.

### Failure artifacts

On test failure, `NewPage` automatically captures a screenshot and HTML dump into `testdata/e2e-artifacts/`, relative to the test package — `e2e/testdata/e2e-artifacts/` from the repo root. Override with `E2E_ARTIFACT_DIR`. You can also capture manually:

```go
e2e.SaveScreenshot(t, page, "before-submit")
e2e.SavePageHTML(t, page, "form-state")
```

## CI setup

### GitHub Actions

The scaffolded `.github/workflows/ci.yml` runs `make test` but **not** E2E. To add it, append after the test step (the runner needs Docker, which `ubuntu-latest` has):

```yaml title=".github/workflows/ci.yml"
- name: Run E2E tests
  env:
    E2E_HEADLESS: "true"
    E2E_TIMEOUT: "30s"
  run: make e2e

- name: Upload failure artifacts
  if: failure()
  uses: actions/upload-artifact@v7
  with:
    name: e2e-artifacts
    path: e2e/testdata/e2e-artifacts/
```

### Verifying generated static pages

The scaffolded CI already verifies that committed [static pages](static-assets.md#static-page-generation) match the build:

```yaml title=".github/workflows/ci.yml"
- name: Verify generated static pages are committed
  run: |
    if ! git diff --quiet -- 'generated/'; then
      echo "::error::Generated static pages are out of date. Run 'make generate' and commit."
      exit 1
    fi
```

This works because `make build` runs `--generate` as part of its build step. See [Static assets](static-assets.md#ci-verification) for the matching `frontend/dist/` check.
