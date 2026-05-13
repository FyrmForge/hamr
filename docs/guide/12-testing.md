# Testing

HAMR's architecture — handlers depend on interfaces, validators are pure functions, repositories are thin DB wrappers — makes testing straightforward at every layer. This guide covers unit testing patterns, E2E testing with go-rod, and CI setup.

**Package references:** [E2E](pkg/e2e.md)

---

## Unit Testing

### Running Tests

Always use the Makefile:

```bash
make test
```

### Handler Tests

Test handlers by creating mock dependencies and asserting on responses. Since handlers depend on interfaces (repos, storage), you can use hand-written mocks or a mocking library:

```go
func TestHomeHandler(t *testing.T) {
    e := echo.New()
    req := httptest.NewRequest(http.MethodGet, "/", nil)
    rec := httptest.NewRecorder()
    c := e.NewContext(req, rec)

    // mockRepo implements the same interface your real repo does
    h := NewHandler(mockRepo)
    err := h.Home(c)

    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, rec.Code)
}
```

### Repository Tests

Test against a local Postgres instance (started by Docker Compose) or use testcontainers for isolated database tests:

```go
func TestUserRepo_Create(t *testing.T) {
    // Connect to a test database (e.g., from DATABASE_URL env var or testcontainers)
    database, err := db.Connect(os.Getenv("TEST_DATABASE_URL"))
    require.NoError(t, err)
    t.Cleanup(func() { database.Close() })

    repo := NewUserRepo(database)
    err = repo.Create(context.Background(), &User{
        Name:  "Alice",
        Email: "alice@example.com",
    })
    require.NoError(t, err)
}
```

For SQLite projects, use a `:memory:` database for fast, isolated per-test setup — no Docker, no filesystem cleanup:

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

func TestUserRepo_Create(t *testing.T) {
    database, err := sqlite.ConnectContext(context.Background(), ":memory:")
    require.NoError(t, err)
    t.Cleanup(func() { database.Close() })

    require.NoError(t, sqlite.Migrate(database, sqlite.MigrateConfig{
        FS:        migrationsFS,
        Directory: "migrations",
    }))

    repo := NewUserRepo(database)
    // ...
}
```

Each `:memory:` connection is an independent database — parallel tests don't share state.

### Validator Tests

Validators are pure functions — easy to test:

```go
func TestEmail(t *testing.T) {
    assert.Empty(t, validate.Email("user@example.com"))
    assert.NotEmpty(t, validate.Email("not-an-email"))
    assert.NotEmpty(t, validate.Email(""))
}
```

---

## E2E Testing

HAMR provides go-rod browser helpers for end-to-end tests.

### Setup

The `//go:build e2e` constraint ensures these tests only run when you pass `-tags=e2e`, keeping them out of `make test`.

```go
//go:build e2e

package e2e_test

import (
    "testing"
    "github.com/FyrmForge/hamr/pkg/e2e"
)

func TestDashboard(t *testing.T) {
    browser := e2e.SetupBrowser(t)
    page := e2e.NewPage(t, browser, baseURL+"/dashboard")

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

### Interaction Helpers

```go
e2e.Input(t, page, "#email", "alice@example.com")
e2e.Click(t, page, "button[type=submit]")
e2e.SelectOption(t, page, "#country", "US")
e2e.WaitForElement(t, page, ".toast-success", 5*time.Second)
e2e.WaitForURLChange(t, page, currentURL, 5*time.Second)
```

### Assertions

All assertions use `assert` (non-fatal) so multiple checks report all failures:

```go
e2e.AssertElementExists(t, page, ".welcome-banner")
e2e.AssertElementNotExists(t, page, ".deleted-item")
e2e.AssertElementContainsText(t, page, "h1", "Dashboard")
e2e.AssertURL(t, page, baseURL+"/dashboard")
e2e.AssertURLContains(t, page, "/dashboard")
e2e.AssertElementCount(t, page, ".user-row", 5)
```

### HTMX-Aware Waiters

Standard page-load waits don't work with HTMX because swaps happen after the initial page load via asynchronous XHR requests. Use these helpers:

```go
e2e.Click(t, page, "#load-more")
e2e.WaitForHTMXIdle(t, page, 5*time.Second)
e2e.AssertElementExists(t, page, ".new-items")

// Or in one step:
e2e.ClickAndWaitHTMX(t, page, "#delete-item", 5*time.Second)
```

### Failure Artifacts

On test failure, `NewPage` automatically captures a screenshot and HTML dump. You can also capture manually:

```go
e2e.SaveScreenshot(t, page, "before-submit")
e2e.SavePageHTML(t, page, "form-state")
```

---

## Integration Tests

For tests that need a real database, [testcontainers-go](https://github.com/testcontainers/testcontainers-go) can spin up a Postgres instance in Docker per-test. HAMR's own `pkg/db` package uses this approach for connection resilience tests.

For SQLite projects, prefer `:memory:` over testcontainers — it's faster and has no external dependencies. Use a temp file (`filepath.Join(t.TempDir(), "test.db")`) only when a test specifically exercises on-disk behaviour like WAL checkpointing or file locking.

---

## CI Setup

### GitHub Actions

```yaml
- name: Run tests
  run: make test

- name: Run E2E tests
  env:
    E2E_HEADLESS: "true"
    E2E_TIMEOUT: "30s"
    E2E_ARTIFACT_DIR: "test-artifacts"
  run: go test -v -tags=e2e ./e2e/ -timeout 10m

- name: Upload failure artifacts
  if: failure()
  uses: actions/upload-artifact@v4
  with:
    name: e2e-artifacts
    path: test-artifacts/
```

### Verify Generated Static Pages

If your project uses [static page generation](09-static-assets.md#static-page-generation), CI should verify the committed files match what the build produces:

```yaml
- name: Verify generated pages are committed
  run: |
    if ! git diff --quiet -- 'generated/'; then
      echo "::error::Generated static pages are out of date."
      exit 1
    fi
```

This works because `make build` already runs `--generate` as part of its build step.

---

## Next Steps

- [Deployment](13-deployment.md) — Docker build, CI/CD, production config
- [Project Setup](01-project-setup.md) — Makefile targets reference
