# HTMX — Request Detection & Response Headers

`hamr/pkg/htmx` provides request detection and response header helpers for htmx. All
functions operate on standard `net/http` types with no framework dependency.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/htmx"
```

## Request Detection

Check whether a request was made by htmx:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    if htmx.IsHTMX(r) {
        // render partial HTML
    } else {
        // render full page
    }
}
```

### Boosted requests

```go
if htmx.IsBoosted(r) {
    // hx-boost navigation — render full page layout
}
```

### Request metadata

```go
trigger := htmx.GetTrigger(r)  // element that triggered the request
target  := htmx.GetTarget(r)   // target element for the response
```

## Response Headers

Control htmx behaviour from the server side.

### Redirect

Client-side redirect (no full page reload):

```go
htmx.Redirect(w, "/dashboard")
```

### Trigger events

Fire custom events on the client after the response:

```go
htmx.Trigger(w, "userCreated", "showToast")
htmx.TriggerAfterSettle(w, "formReset")
htmx.TriggerAfterSwap(w, "scrollToTop")
```

### Swap control

Override the swap strategy or target:

```go
htmx.Reswap(w, "outerHTML")
htmx.Retarget(w, "#error-container")
```

### Page refresh

Force a full page refresh:

```go
htmx.Refresh(w)
```

### URL manipulation

Update the browser URL bar without navigation:

```go
htmx.PushURL(w, "/users/42")
htmx.ReplaceURL(w, "/users/42")
```

## Header Constants

All htmx header names are exported as constants for use in tests or custom logic:

### Request headers

| Constant | Value |
|----------|-------|
| `HeaderRequest` | `HX-Request` |
| `HeaderBoosted` | `HX-Boosted` |
| `HeaderTrigger` | `HX-Trigger` |
| `HeaderTarget` | `HX-Target` |
| `HeaderCurrentURL` | `HX-Current-URL` |
| `HeaderPrompt` | `HX-Prompt` |
| `HeaderHistoryRestore` | `HX-History-Restore-Request` |

### Response headers

| Constant | Value |
|----------|-------|
| `HeaderRedirect` | `HX-Redirect` |
| `HeaderTriggerResponse` | `HX-Trigger` |
| `HeaderTriggerAfterSettle` | `HX-Trigger-After-Settle` |
| `HeaderTriggerAfterSwap` | `HX-Trigger-After-Swap` |
| `HeaderReswap` | `HX-Reswap` |
| `HeaderRetarget` | `HX-Retarget` |
| `HeaderRefresh` | `HX-Refresh` |
| `HeaderPushURL` | `HX-Push-Url` |
| `HeaderReplaceURL` | `HX-Replace-Url` |

## API Reference

```go
// Request detection
func IsHTMX(r *http.Request) bool
func IsBoosted(r *http.Request) bool
func GetTrigger(r *http.Request) string
func GetTarget(r *http.Request) string

// Response headers
func Redirect(w http.ResponseWriter, url string)
func Trigger(w http.ResponseWriter, events ...string)
func TriggerAfterSettle(w http.ResponseWriter, events ...string)
func TriggerAfterSwap(w http.ResponseWriter, events ...string)
func Reswap(w http.ResponseWriter, strategy string)
func Retarget(w http.ResponseWriter, selector string)
func Refresh(w http.ResponseWriter)
func PushURL(w http.ResponseWriter, url string)
func ReplaceURL(w http.ResponseWriter, url string)
```
