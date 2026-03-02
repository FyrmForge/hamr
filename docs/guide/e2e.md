# E2E — Reusable Go-Rod Browser Helpers

`hamr/pkg/e2e` provides timeout-safe go-rod helpers for browser-based E2E testing.
Projects import this package in their `e2e-go/` test files. The package itself has no
`//go:build` tag — it's a normal library. Build-tag isolation happens in the consuming
project.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/e2e"
```

## Design

Every browser operation uses explicit timeouts — **no `Must*` methods** anywhere. This
prevents tests from hanging indefinitely in CI.

```go
// Always timeout-safe
element, err := page.Timeout(cfg.Timeout).Element("#selector")
require.NoError(t, err)

// Never use — can hang forever
page.MustElement("#selector")
```

Functions split into two categories by failure behaviour:

| Category | Functions | On failure |
|----------|-----------|------------|
| Setup / Interaction | `SetupBrowser`, `NewPage`, `Input`, `Click`, etc. | `require` — fatal, stops test |
| Assertions | `AssertElementExists`, `AssertURL`, etc. | `assert` — non-fatal, reports all failures |
| Artifact capture | `SaveScreenshot`, `SavePageHTML` | `t.Logf` — never fails the test |
| HTMX waiters | `WaitForHTMXIdle`, `WaitForHTMXSwap`, `ClickAndWaitHTMX` | `t.Errorf` — non-fatal |

## Configuration

`Config` controls browser lifecycle, timeouts, and artifact capture. Resolution order:
**defaults -> code options -> env var overrides** (env always wins).

```go
browser := e2e.SetupBrowser(t,
    e2e.WithHeadless(false),
    e2e.WithSlowMotion(500*time.Millisecond),
    e2e.WithTimeout(15*time.Second),
)
```

| Option | Env Var | Default | Description |
|--------|---------|---------|-------------|
| `WithHeadless(bool)` | `E2E_HEADLESS` | `true` | `false` for local debugging with visible browser |
| `WithSlowMotion(d)` | `E2E_SLOW_MOTION` | `0` | Delay between actions (useful with headed mode) |
| `WithTimeout(d)` | `E2E_TIMEOUT` | `10s` | Default timeout for all browser operations |
| `WithArtifactDir(path)` | `E2E_ARTIFACT_DIR` | `testdata/e2e-artifacts` | Where failure screenshots/HTML are saved |
| `WithScreenshotOnFailure(bool)` | `E2E_SCREENSHOT_ON_FAIL` | `true` | Auto-capture PNG on test failure |
| `WithHTMLDumpOnFailure(bool)` | `E2E_HTML_DUMP_ON_FAIL` | `true` | Auto-capture page DOM on test failure |

Env vars let CI override behaviour without code changes:

```bash
E2E_HEADLESS=true E2E_TIMEOUT=30s go test -tags=e2e ./e2e-go/
```

Config is stored per-test via `sync.Map` keyed by `testing.TB`, so helpers can retrieve
timeout and artifact dir without explicit config passing. `SetupBrowser` stores it;
`t.Cleanup` deletes it.

## Browser Lifecycle

### SetupBrowser — launch Chromium

Launches a headless Chromium instance with Linux-safe flags (`NoSandbox`,
`disable-dev-shm-usage`, `disable-gpu`). Uses error-returning `Launch()` and
`Connect()` — never `Must*`. Registers `t.Cleanup` to close the browser.

```go
func TestDashboard(t *testing.T) {
    browser := e2e.SetupBrowser(t)
    page := e2e.NewPage(t, browser, "http://localhost:3000/dashboard")

    e2e.AssertElementExists(t, page, ".welcome-banner")
}
```

### NewPage — open a tab

Creates a new browser tab, navigates to the URL, and waits for load within
`cfg.Timeout`. On `t.Failed()`, the cleanup automatically captures a screenshot and
HTML dump (if enabled). Then closes the page.

```go
page := e2e.NewPage(t, browser, "http://localhost:3000/dashboard")
```

Artifacts are named `<TestName>_<timestamp>.png` / `.html` and saved to `cfg.ArtifactDir`.

## Interaction Helpers

All interaction helpers use `require` — they fatal on failure so subsequent steps don't
run against a broken page.

### Input

Clears and types a value into the element matching a CSS selector.

```go
e2e.Input(t, page, "#name", "Alice")
e2e.Input(t, page, "#email", "alice@example.com")
```

### Click

Clicks the element matching a CSS selector.

```go
e2e.Click(t, page, "button[type=submit]")
e2e.Click(t, page, ".nav-link.logout")
```

### SelectOption

Selects a `<option>` by value inside a `<select>`.

```go
e2e.SelectOption(t, page, "#country", "US")
```

### WaitForElement

Polls for an element to appear within a custom timeout.

```go
el := e2e.WaitForElement(t, page, ".toast-success", 5*time.Second)
```

### WaitForURLChange

Waits until the page URL differs from the given URL.

```go
e2e.WaitForURLChange(t, page, "http://localhost:3000/login", 5*time.Second)
```

### ElementExists / ElementNotExists

Non-fatal existence checks. Return `bool` instead of failing the test.

```go
if e2e.ElementExists(t, page, ".optional-banner") {
    // banner is present
}
if e2e.ElementNotExists(t, page, ".deleted-item") {
    // element is gone
}
```

`ElementNotExists` uses a short timeout (500ms) to avoid waiting the full config
timeout for an element that is expected to be absent.

### ElementText

Grab text content without asserting — useful for logging, conditional logic, or storing
a value to compare later.

```go
price := e2e.ElementText(t, page, ".total-price")
t.Logf("total price: %s", price)
```

### ElementAttribute

Read an attribute like `href`, `value`, `data-*`, `disabled`. Returns empty string if
the attribute is not set.

```go
href := e2e.ElementAttribute(t, page, ".profile-link", "href")
disabled := e2e.ElementAttribute(t, page, "#submit", "disabled")
dataID := e2e.ElementAttribute(t, page, ".user-row", "data-user-id")
```

### ElementCount

Count how many elements match a selector — useful for tables, lists, search results.

```go
count := e2e.ElementCount(t, page, ".user-row")
t.Logf("found %d users", count)
```

Returns 0 if no elements match (never fatals).

### WaitForElementRemoved

Wait for an element to disappear from the page. Unlike `ElementNotExists` which checks
once with a short timeout, this polls until the element is gone or timeout elapses.
Useful for loading spinners, deleted rows, dismissing modals.

```go
e2e.Click(t, page, "#delete-user")
e2e.WaitForElementRemoved(t, page, "#user-row-42", 5*time.Second)
```

## Assertions

All assertions use `assert` (non-fatal) so multiple checks report all failures. All
call `t.Helper()` for clean stack traces.

```go
e2e.AssertElementExists(t, page, ".welcome-banner")
e2e.AssertElementNotExists(t, page, ".deleted-item")
e2e.AssertElementContainsText(t, page, "h1", "Dashboard")
e2e.AssertURL(t, page, "http://localhost:3000/dashboard")
e2e.AssertURLContains(t, page, "/dashboard")
e2e.AssertElementNotVisible(t, page, ".error-message")
e2e.AssertElementHasClass(t, page, ".nav-item:first-child", "active")
e2e.AssertElementCount(t, page, ".user-row", 5)
```

## HTMX-Aware Waiters

For pages using HTMX, standard page-load waits don't work because HTMX swaps content
without full page loads. These helpers bridge that gap.

### WaitForHTMXIdle

Polls until no elements carry the `htmx-request` class, indicating all in-flight HTMX
requests have settled. Falls back immediately if htmx is not loaded on the page.

```go
e2e.Click(t, page, "#load-more")
e2e.WaitForHTMXIdle(t, page, 5*time.Second)
e2e.AssertElementExists(t, page, ".new-items")
```

### WaitForHTMXSwap

Waits until the HTML content of a specific element changes, indicating an HTMX swap
occurred.

```go
e2e.Click(t, page, "#refresh-stats")
e2e.WaitForHTMXSwap(t, page, "#stats-panel", 5*time.Second)
e2e.AssertElementContainsText(t, page, "#stats-panel", "Updated")
```

### ClickAndWaitHTMX

Convenience: clicks an element and waits for HTMX to settle.

```go
e2e.ClickAndWaitHTMX(t, page, "#delete-item", 5*time.Second)
e2e.AssertElementNotVisible(t, page, "#item-row-42")
```

## Artifact Capture

Screenshots and HTML dumps are captured automatically on test failure via `NewPage`'s
cleanup. You can also capture them manually at any point:

```go
e2e.SaveScreenshot(t, page, "before-submit")
e2e.SavePageHTML(t, page, "form-state")
```

Artifact functions never fail the test — they log warnings on error. Filenames are
sanitized and timestamped: `before-submit_1709312400000.png`.

## Full Example

```go
//go:build e2e

package e2e_test

import (
    "testing"
    "time"

    "github.com/FyrmForge/hamr/pkg/e2e"
)

func TestDashboard(t *testing.T) {
    browser := e2e.SetupBrowser(t)
    page := e2e.NewPage(t, browser, baseURL+"/dashboard")

    // Verify page content
    e2e.AssertElementExists(t, page, ".welcome-banner")
    e2e.AssertElementContainsText(t, page, "h1", "Welcome")

    // Test HTMX interaction
    e2e.ClickAndWaitHTMX(t, page, "#load-notifications", 5*time.Second)
    e2e.AssertElementExists(t, page, ".notification-list")
}
```

## CI Tips

Override defaults via env vars in your CI pipeline:

```yaml
# GitHub Actions example
- name: Run E2E tests
  env:
    E2E_HEADLESS: "true"
    E2E_TIMEOUT: "30s"
    E2E_ARTIFACT_DIR: "test-artifacts"
  run: go test -v -tags=e2e ./e2e-go/ -timeout 10m

- name: Upload failure artifacts
  if: failure()
  uses: actions/upload-artifact@v4
  with:
    name: e2e-artifacts
    path: test-artifacts/
```

## API Reference

```go
// Configuration
type Config struct { ... }
type Option func(*Config)
func WithHeadless(b bool) Option
func WithSlowMotion(d time.Duration) Option
func WithTimeout(d time.Duration) Option
func WithArtifactDir(path string) Option
func WithScreenshotOnFailure(b bool) Option
func WithHTMLDumpOnFailure(b bool) Option

// Browser lifecycle
func SetupBrowser(t *testing.T, opts ...Option) *rod.Browser
func NewPage(t *testing.T, browser *rod.Browser, url string) *rod.Page

// Interaction helpers
func WaitForElement(t *testing.T, page *rod.Page, selector string, timeout time.Duration) *rod.Element
func WaitForURLChange(t *testing.T, page *rod.Page, currentURL string, timeout time.Duration)
func Input(t *testing.T, page *rod.Page, selector, value string)
func Click(t *testing.T, page *rod.Page, selector string)
func SelectOption(t *testing.T, page *rod.Page, selector, value string)
func ElementExists(t *testing.T, page *rod.Page, selector string) bool
func ElementNotExists(t *testing.T, page *rod.Page, selector string) bool
func ElementText(t *testing.T, page *rod.Page, selector string) string
func ElementAttribute(t *testing.T, page *rod.Page, selector, attr string) string
func ElementCount(t *testing.T, page *rod.Page, selector string) int
func WaitForElementRemoved(t *testing.T, page *rod.Page, selector string, timeout time.Duration)
func SaveScreenshot(t *testing.T, page *rod.Page, name string)
func SavePageHTML(t *testing.T, page *rod.Page, name string)

// Assertions (non-fatal)
func AssertElementExists(t *testing.T, page *rod.Page, selector string)
func AssertElementNotExists(t *testing.T, page *rod.Page, selector string)
func AssertElementNotVisible(t *testing.T, page *rod.Page, selector string)
func AssertElementContainsText(t *testing.T, page *rod.Page, selector, text string)
func AssertURL(t *testing.T, page *rod.Page, expected string)
func AssertURLContains(t *testing.T, page *rod.Page, substring string)
func AssertElementHasClass(t *testing.T, page *rod.Page, selector, class string)
func AssertElementCount(t *testing.T, page *rod.Page, selector string, expected int)

// HTMX-aware waiters (non-fatal on timeout)
func WaitForHTMXIdle(t *testing.T, page *rod.Page, timeout time.Duration)
func WaitForHTMXSwap(t *testing.T, page *rod.Page, selector string, timeout time.Duration)
func ClickAndWaitHTMX(t *testing.T, page *rod.Page, selector string, timeout time.Duration)
```
