# HAMR Components — Design

> Status: **Draft**, iteration 13 (htmx-native pivot)
> Supersedes: `_archived-hamr-live/`, iterations 1–12
> Last updated: 2026-05-19

---

## Decision (TL;DR)

A hamr "component" is just a plain templ function with a small concurrency primitive. No framework registry, no identity hashing, no slot machinery, no server-pushed HTML. WebSocket reactivity uses the existing `pkg/websocket` infrastructure directly.

The framework adds **two helpers and one JS snippet**. Everything else is normal hamr + templ + Echo code.

## What's in scope

- A goroutine wrapper (`hamr.Load`) that lets sibling components fetch data concurrently and `templ` await them at render time.
- A templ helper (`hamr.Reactive`, optional sugar) that emits the htmx attrs for WS-triggered refresh.
- A small JS bridge added to `pkg/websocket/ws.js.tmpl` that turns WS topic messages into DOM events htmx can listen for.
- A CLI scaffolder (`hamr add comp <name>`) that generates a component package and patches the central `Components` struct.
- A `hamr.toml` key for the component package directory.

## What's NOT in scope

- No `Component` interface, no `Subscriber` interface, no `Registry`.
- No identity hashing, no slot paths, no `Embed`, no `View[P,T]` wrapper, no generic `Register`.
- No `Mount`, `Actions()` allow-list, `Authorize`, `OnRefresh`, `PostCommit`.
- No `Store` abstraction, no advisory locks, no envelope/TTL/version machinery.
- No server-side push of component HTML. Server pushes topic names; client re-fetches via htmx.
- No new client runtime beyond the JS bridge for WS-to-DOM-event.

---

## 1. When to use components (the one-sentence rule)

> **Reach for a component when a chunk of a page has its own server-side data fetch and you want it to live as its own package — optionally re-rendering on a server-side event. Otherwise, write a templ partial.**

A component differs from a plain templ partial in two ways:
1. It owns its server logic (data fetching, optional action endpoints).
2. It can be refreshed independently when an event fires.

If a fragment is pure markup with no per-instance fetch, use a templ partial. Don't dress it up as a component.

---

## 2. Usage

### 2.1 Anatomy

Each component is its own package under whatever directory `hamr.toml` configures (default `internal/components/`):

```
internal/components/salestile/
  salestile.templ    // pure templ render
  salestile.go       // Component struct, View, action handlers
  salestile_test.go
```

### 2.2 Component implementation

```go
package salestile

import (
    "context"
    "database/sql"

    "github.com/a-h/templ"
    "github.com/labstack/echo/v4"
    "yourorg/hamr/pkg/hamr"
    "yourorg/hamr/pkg/websocket"
)

type Props struct {
    Range string
}

type Component struct {
    db  *sql.DB
    bus *websocket.Emitter
}

func New(db *sql.DB, bus *websocket.Emitter) *Component {
    return &Component{db: db, bus: bus}
}

// View is the templ-callable entry point.
// Calling it kicks off a goroutine that fetches data; the returned
// templ.Component awaits the result at render time.
func (c *Component) View(e echo.Context, p Props) templ.Component {
    return hamr.Load(e, func(e echo.Context) templ.Component {
        ctx := e.Request().Context()
        data, err := c.fetch(ctx, p.Range)
        if err != nil {
            return errorView(err)
        }
        return view(p, data)
    })
}

// fetch is private — the component owns its data layer.
func (c *Component) fetch(ctx context.Context, rng string) (Data, error) {
    // db queries here, possibly using errgroup for further concurrency
}

// RegisterActions wires the component's action endpoints.
// Called once at app boot.
func (c *Component) RegisterActions(e *echo.Echo) {
    g := e.Group("/__hamr/comp/sales-tile")
    g.POST("/refresh-cache", c.refreshCache)
}

func (c *Component) refreshCache(e echo.Context) error {
    // mutation
    // fire a WS event so other tabs / other users see the update
    c.bus.SendToSubject(uid, websocket.NewTriggerEvent("hamr:sales-updated", "body"))
    return e.NoContent(http.StatusOK)
}
```

`view` is a pure templ function over `Props + Data`:

```templ
package salestile

templ view(p Props, d Data) {
    @hamr.Reactive("sales-tile-" + p.Range, "sales-updated") {
        <div>Sales ({ p.Range }): { strconv.Itoa(d.Total) }</div>
        <button hx-post="/__hamr/comp/sales-tile/refresh-cache" hx-target="closest div">Refresh</button>
    }
}
```

### 2.3 Wiring at boot

App-level `Components` struct (typically in `internal/components/components.go`):

```go
package components

type Components struct {
    Sales   *salestile.Component
    Account *accountcomp.Component
    Users   *userscomp.Component
}

func New(d Deps) *Components {
    return &Components{
        Sales:   salestile.New(d.DB, d.Emitter),
        Account: accountcomp.New(d.DB, d.Emitter),
        Users:   userscomp.New(d.DB, d.Emitter),
    }
}

// One call to wire every component's action routes.
func (cs *Components) RegisterActions(e *echo.Echo) {
    cs.Sales.RegisterActions(e)
    cs.Account.RegisterActions(e)
    cs.Users.RegisterActions(e)
}
```

In `main.go`:

```go
comps := components.New(deps)
comps.RegisterActions(e)
```

### 2.4 Using components in a page

Page handler:

```go
type pageHandler struct {
    comps *components.Components
    db    *sql.DB
}

func (h *pageHandler) Dashboard(e echo.Context) error {
    props, err := h.db.GetPageInfo(e.Request().Context())
    if err != nil {
        return err
    }
    return respond.HTML(e, dashboardPage(e, props, h.comps))
}
```

Page templ:

```templ
templ dashboardPage(e echo.Context, props PageProps, c *components.Components) {
    @layout.Page(e) {
        <div class="grid">
            @c.Sales.View(e,   salestile.Props{Range: "7d"})
            @c.Account.View(e, accountcomp.Props{AccountID: props.AccountID})
            @c.Users.View(e,   userscomp.Props{})
        </div>
        <div>{ props.Title }</div>
    }
}
```

Each `View(e, ...)` call kicks off a goroutine immediately. All three goroutines run concurrently. The parent's render awaits each in turn — total time ≈ slowest component, not sum.

### 2.5 The three patterns

The component-as-struct-method shape above is the default. Two variants for specific cases:

#### Inline (no struct bundle)

When a page only needs one or two components and you don't want to thread a bundle through:

```templ
templ Page(e echo.Context, props Props) {
    @signupcount.View(e, signupcount.Props{}, deps)
    @recentorders.View(e, recentorders.Props{Limit: 5}, deps)
}
```

Component package exposes a top-level function instead of (or alongside) the struct:

```go
func View(e echo.Context, p Props, deps Deps) templ.Component {
    return hamr.Load(e, func(e echo.Context) templ.Component {
        // ...
    })
}
```

Use when: bundling feels heavier than the gain. Re-converge to the struct pattern when the app grows.

#### Ctx-stash (singleton-per-request)

For values that exist exactly once per request — current user, current account, sidebar navigation. Loaded by middleware, pulled by templ:

```go
package currentaccount

type ctxKey struct{}

type Props struct{ AccountID int }

func With(e echo.Context, db *sql.DB, p Props) {
    ch := make(chan templ.Component, 1)
    go func() {
        ctx := e.Request().Context()
        data, err := loadAccount(ctx, db, p.AccountID)
        if err != nil { ch <- errorView(err); return }
        ch <- view(p, data)
    }()
    e.Set("hamr.currentaccount", ch)
}

func Render(e echo.Context) templ.Component {
    ch, _ := e.Get("hamr.currentaccount").(chan templ.Component)
    if ch == nil { return nothing() }
    return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
        return (<-ch).Render(ctx, w)
    })
}

func Middleware(deps Deps) echo.MiddlewareFunc {
    return func(next echo.HandlerFunc) echo.HandlerFunc {
        return func(e echo.Context) error {
            With(e, deps.DB, Props{AccountID: auth.UserID(e)})
            return next(e)
        }
    }
}
```

Wire once in app boot:

```go
e.Use(currentaccount.Middleware(deps))
```

Template anywhere uses it without threading:

```templ
@currentaccount.Render(e)
```

Use when: the component is genuinely a singleton per request. Don't use for components instantiated multiple times per page — use the bundle.

### 2.6 Concurrent fetching

The `hamr.Load` primitive starts a goroutine when called and returns a `templ.Component` that awaits the result at render time:

```go
func Load(e echo.Context, fn func(e echo.Context) templ.Component) templ.Component {
    ch := make(chan templ.Component, 1)
    go func() { ch <- fn(e) }()
    return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
        return (<-ch).Render(ctx, w)
    })
}
```

That's the whole implementation. Sibling `View` calls all kick off `hamr.Load` simultaneously; their goroutines race; render walks them sequentially, awaiting each.

For parallelism with the page handler's own work, call `View` in the handler and pass the result to the templ:

```go
func (h *pageHandler) Dashboard(e echo.Context) error {
    sales := h.comps.Sales.View(e, salestile.Props{Range: "7d"})       // goroutine starts
    users := h.comps.Users.View(e, userscomp.Props{})                  // goroutine starts

    props, err := h.db.GetPageInfo(e.Request().Context())              // runs in parallel
    if err != nil {
        return err
    }
    return respond.HTML(e, dashboardPage(e, props, sales, users))
}
```

```templ
templ dashboardPage(e echo.Context, props Props, sales, users templ.Component) {
    @layout.Page(e) { @sales @users }
}
```

### 2.7 WS reactivity

WS reactivity uses **existing `pkg/websocket` infrastructure**. No component-specific protocol.

#### Subscribing (in the component's templ)

```templ
@hamr.Reactive("sales-tile-" + p.Range, "sales-updated") {
    ...content...
}
```

`hamr.Reactive` emits:

```html
<div id="sales-tile-7d"
     hx-trigger="hamr:sales-updated from:body"
     hx-get="/__hamr/comp/sales-tile/refresh-cache"
     hx-target="this"
     hx-swap="outerHTML">
  ...
</div>
```

You can write the attrs by hand if `Reactive` doesn't fit; it's sugar, not a framework hook.

#### Publishing (anywhere)

Use the existing emitter:

```go
deps.Emitter.SendToSubject(userID, websocket.NewTriggerEvent("hamr:sales-updated", "body"))
```

#### Bridge

A small JS snippet added to `pkg/websocket/ws.js.tmpl` listens for incoming WS events of type `trigger` and dispatches a corresponding DOM `CustomEvent` on `body`. htmx picks it up via `hx-trigger="hamr:* from:body"`.

#### Pushing HTML (alternative)

For cases where the server *does* want to push the rendered HTML directly (avoiding the round-trip), the existing `websocket.NewOuterHTMLEvent` still works. Components don't depend on it being used; it's available when the latency saving matters.

### 2.8 Actions

Actions are normal Echo POST routes registered by the component:

```go
func (c *Component) RegisterActions(e *echo.Echo) {
    g := e.Group("/__hamr/comp/sales-tile")
    g.POST("/refresh-cache", c.refreshCache)
    g.POST("/clear-totals", c.clearTotals)
}
```

Templ buttons use them:

```templ
<button hx-post="/__hamr/comp/sales-tile/refresh-cache"
        hx-target="closest div"
        hx-swap="outerHTML">
    Refresh
</button>
```

Action handlers may fire WS triggers so other clients re-render:

```go
func (c *Component) refreshCache(e echo.Context) error {
    c.bus.SendToSubject(uid, websocket.NewTriggerEvent("hamr:sales-updated", "body"))
    return respond.HTML(e, c.View(e, Props{Range: "7d"}))  // also return the refreshed view
}
```

CSRF, rate limiting, auth, validation — all inherited from the app's existing middleware stack. The framework does not own any of this.

---

## 3. Implementation

### 3.1 `hamr.Load`

```go
// pkg/hamr/load.go

func Load(e echo.Context, fn func(e echo.Context) templ.Component) templ.Component {
    ch := make(chan templ.Component, 1)
    go func() { ch <- fn(e) }()
    return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
        return (<-ch).Render(ctx, w)
    })
}
```

**Lifetime**: the goroutine completes before the templ render reads from the channel (render blocks on receive). Accessing `echo.Context` inside the goroutine is safe within this lifecycle.

**Cancellation**: if the request's context is cancelled (client disconnect), the dev's code inside `fn` should respect `e.Request().Context()` for any DB / HTTP calls. The goroutine itself isn't explicitly cancelled by the framework — it runs to completion and writes to the channel, which is buffered so no leak.

**Errors**: `fn` returns `templ.Component`. The convention is to return `errorView(err)` (a pure templ component) on failure. The framework does not have an error type; errors render as HTML.

### 3.2 `hamr.Reactive`

```templ
templ Reactive(id string, event string) {
    <div id={ id }
         hx-trigger={ "hamr:" + event + " from:body" }
         hx-get={ /* configured per call site or convention */ }
         hx-target="this"
         hx-swap="outerHTML">
        { children... }
    </div>
}
```

Open question: how should `hx-get` be derived? Three options:

1. Pass it explicitly: `@hamr.Reactive(id, event, "/__hamr/comp/sales-tile/refresh")`.
2. Derive from convention: `/__hamr/comp/<id-prefix>`.
3. Use the same page URL with `?__hamr=<id>` and let the page handler dispatch.

**Decision needed** (see §5).

### 3.3 WS-to-DOM-event bridge

Added to `pkg/websocket/ws.js.tmpl`:

```javascript
// In the existing onmessage handler:
if (event.type === 'trigger' && event.trigger) {
    const target = event.target ? document.querySelector(event.target) : document.body;
    if (target) {
        target.dispatchEvent(new CustomEvent(event.trigger, { bubbles: true }));
    }
}
```

This relies on `websocket.NewTriggerEvent` already setting `type: "trigger"`, `trigger: "name"`, `target: "selector"` on its JSON shape (it does — see `pkg/websocket/event.go`).

### 3.4 CLI scaffolder

`hamr add comp <name>` produces:

```
<comp_dir>/<name>/
  <name>.templ   // skeleton with hamr.Reactive wrapper
  <name>.go      // Props, Component struct, New, View, RegisterActions stub
  <name>_test.go // empty test scaffold
```

And patches `<comp_dir>/components.go`:
- Adds field `<Name> *<name>.Component` to `Components`.
- Adds wiring line to `New(deps)`.
- Adds `cs.<Name>.RegisterActions(e)` to `RegisterActions`.

Patching uses simple text markers (not AST) — `// HAMR-COMP-FIELDS`, `// HAMR-COMP-WIRING`, `// HAMR-COMP-ACTIONS`. If markers are missing, the command prints the lines for the dev to paste.

### 3.5 `hamr.toml` schema

```toml
[components]
dir = "internal/components/"
```

Path is relative to the project root. Defaults to `internal/components/`.

---

## 4. Phasing

### v1 — everything in this doc

Deliverables:
- `pkg/hamr/load.go` — `Load` helper.
- `pkg/hamr/reactive.templ` — `Reactive` helper (sugar).
- Added JS in `pkg/websocket/ws.js.tmpl` — WS-to-DOM-event bridge.
- `hamr add comp <name>` CLI command.
- `hamr.toml` `[components].dir` key.
- Docs in `docs/guide/` covering the three patterns and a complete dashboard example.
- Updated `llmsdocs/llms.txt` + `llmsdocs/llms-full.txt`.

### Not v1

- No "framework-managed component state" of any kind.
- No `Components` interface or registry.
- No tooling beyond the scaffolder (no `componenttest`, no slot linter, no codegen).

---

## 5. Open questions

1. **`hamr.Reactive` `hx-get` derivation.** Explicit param vs convention vs same-page-URL. Probably explicit for v1 (simplest, lowest framework burden).
2. **Default error view.** Should `hamr.Load` accept an optional error fallback? Or leave it entirely to the dev's `fn` body (current spec)?
3. **Cancellation of in-flight goroutines.** Worth wiring an explicit `context.Done()` listener inside `Load` to abort `fn` cleanly, or trust the request context to cascade?
4. **CLI patch resilience.** Text markers are simple but break if the dev edits the central file. Worth a `--print` mode that emits the lines without patching?
5. **`comp_dir` per-namespace.** If a project has admin vs public components in different dirs, should `hamr.toml` accept multiple roots?

---

## Appendix A — What we are NOT doing and why

| Not doing | Why |
|---|---|
| `Component` / `Subscriber` interfaces | Components are plain functions/methods. No interface to satisfy. |
| Identity hashing / slot paths | No framework needs to identify component instances. htmx targets DOM elements by id. |
| Server-pushed HTML on its own initiative | Use `websocket.NewTriggerEvent` to hint, htmx pulls fresh HTML. Existing `NewOuterHTMLEvent` is available for callers that want it. |
| Generic `Embed[P,T]` / `Register[P,T]` | Go methods cannot declare type parameters. Generic wrapper was the wrong abstraction anyway. |
| Server-side state per component | Use DB / session / cookies like normal Go code. |
| `Actions()` allow-list | Actions are explicit POST routes registered in `RegisterActions`. The route IS the allow-list. |
| Codegen / AST scanning | CLI scaffolder uses text templates + text markers. |
| Random component IDs | DOM IDs come from the dev via `hamr.Reactive` (or hand-written). |
| `ScopeSession` / per-tab scope | Not needed — server doesn't track per-instance state. |
| `Mount` / `OnRefresh` / `PostCommit` / `Authorize` | No state to mount; refresh is htmx; auth is Echo middleware. |
| Store abstraction / locks / envelopes | No state. |
| In-DOM signed snapshots | Not needed. Rejected upfront. |
| New client runtime beyond the JS bridge | htmx + existing hamr WS auto-reconnect is enough. |

---

## Appendix B — Comparison with prior iterations

| Aspect | Iterations 1–11 (stateful) | Iteration 12 (stateless reset) | Iteration 13 (htmx-native, this) |
|---|---|---|---|
| Server-pushed HTML | Yes (via WS) | Yes (via WS) | No — only topic-name hints |
| Registry | Yes | Yes | None |
| Identity / slot paths | Yes | Yes | None |
| `Embed` / `View` wrapper | Yes | Yes | None |
| `Component` interface | `Render(ctx)` + many | `Render(ctx)` + `Subscriber` | None (just functions) |
| Props rehydration | State envelope | Unsolved (BLOCKER) | Re-derived by handler on each refresh GET |
| Concurrency | Per-component coalescing, render queues | Per-component coalescing | One primitive: `hamr.Load` |
| Actions | `Actions()` allow-list + reflection dispatch | None | Plain Echo POST routes |
| Reactivity protocol | Bespoke `hamr.subs` | Bespoke `hamr.subs` | Existing `pkg/websocket` triggers |
| Framework code surface | ~thousands of lines | ~hundreds | ~50 lines (`Load` + bridge JS + scaffolder) |

---

## Appendix C — Revision history

- **Iterations 1–11**: progressively built up server-side stateful component machinery (Mount, Store, locks, atomic Update, advisory locks, envelope versioning, deferred Publish, PostCommit, Authorize). Hostile-reviewer convergence at iteration 11 — "salvageable, somewhere in between."
- **Iteration 12 (radical-KISS reset)**: dropped the entire stateful surface. Kept the registry + typed-Props wrapper + generic Embed. Codex round 11 found two BLOCKERs (Props rehydration impossible without storing props somewhere; `Register[P,T]` method is invalid Go).
- **Iteration 13 (htmx-native pivot)**: the user named the underlying issue — "the server should never push HTML to the client, that makes no sense." Dropped server-pushed HTML. Refresh becomes a normal htmx GET. Props rehydration dissolves because the request handler re-derives Props every time. The whole registry/Embed/Wrapper tower goes with it. Components are plain templ functions + a tiny concurrency primitive (`hamr.Load`). WS reactivity uses existing `pkg/websocket` directly. Three patterns (inline, Components-struct bundle, ctx-stash) documented as conventions, not framework features. Framework surface drops to ~50 lines.
