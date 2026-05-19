# HAMR Components — Proposed Feature

> Status: **Draft, iteration 13** (htmx-native pivot). Pending review.

## What this is

A design proposal for a small set of helpers in `pkg/hamr` (and a CLI scaffolder) that lets app developers write self-contained components: server-side data fetching + templ render, optional WS reactivity, optional action endpoints.

**Not a component framework.** A hamr "component" is just a plain templ function with a goroutine helper for concurrent data fetching. No registry, no identity hashing, no slot paths, no `Embed[P,T]`, no server-pushed HTML. The framework adds two helpers and one JS snippet. Everything else is normal hamr + templ + Echo code.

It supersedes both the archived `_archived-hamr-live/` proposal and iterations 1–12 of this proposal.

## Where to read

1. **[design.md](design.md)** — the full design doc.
2. **critique-round-{1..11}.md** — hostile-reviewer critiques against iterations 1–12. Kept as historical record. Iteration 13 obsoletes most concerns because the surface that triggered them no longer exists.

## How we got here

Iterations 1–11 grew a Phase 2 stateful surface (server-side state, actions, store abstraction, advisory locks, version envelopes, deferred publishes). Converged at "salvageable, somewhere in between" after 10 rounds of hostile critique.

Iteration 12 cut Phase 2 entirely but kept a registry + typed-Props wrapper + generic `Embed`. Codex round 11 found two BLOCKERs: the registry pattern needed to rehydrate `Props` on WS-pushed refresh and couldn't; and `Registry.Register[P,T]` was invalid Go (methods can't declare type parameters).

Iteration 13 named the deeper issue: the server pushing HTML to the client on its own initiative is foreign to hamr. hamr is a pure request/response htmx framework with WebSockets as plain server-push infrastructure. Once refresh becomes "WS sends a topic name, htmx fires a GET, server handles it like any other request", the Props rehydration BLOCKER dissolves (the handler re-derives Props every time from request context). The whole registry/Embed/wrapper tower comes down with it. What remains is ~50 lines of framework code.

## Key decisions (what's IN)

- `hamr.Load(e, fn)` — a 6-line goroutine wrapper. Kicks off `fn` in a goroutine; returns a `templ.Component` that awaits the result at render time.
- `hamr.Reactive(id, event)` — templ helper, optional sugar, emits the htmx attrs for WS-triggered refresh.
- WS-to-DOM-event JS bridge added to `pkg/websocket/ws.js.tmpl`. Turns existing `websocket.NewTriggerEvent`s into DOM events htmx listens for.
- `hamr add comp <name>` CLI scaffolder. Emits the component package and patches a central `Components` struct.
- `hamr.toml` `[components].dir` key. Defaults to `internal/components/`.

## Three patterns (documented as conventions, not framework features)

- **Components-struct bundle** — default. Each component package owns a `*Component` struct (deps captured, methods for View + actions). App boot builds a `Components` bundle. Page handlers receive the bundle.
- **Inline templ call** — when one-off; component package exposes a top-level `View(e, p, deps)` function. No bundle.
- **Ctx-stash** — for singleton-per-request values (current user, current account, sidebar nav). Middleware kicks off the load and stashes on `echo.Context` via a private key. Templ pulls via the package's `Render(e)`.

## Key decisions (what's OUT)

- **No `Component` interface, no `Subscriber`, no `Registry`.**
- **No identity hashing, no slot paths.**
- **No `Embed[P,T]`, no `Register[P,T]`, no generic wrappers.**
- **No server-pushed HTML on framework's initiative.** Server emits topic-name hints; htmx pulls fresh HTML via normal Echo routes.
- **No state machinery.** No `Mount` / `OnRefresh` / `Authorize` / `PostCommit`, no `Store`, no advisory locks, no envelopes.
- **No `Actions()` allow-list.** Actions are plain Echo POST routes; the route is the allow-list.
- **No new client runtime.** Just one JS function added to the existing `ws.js.tmpl`.
- **No codegen / AST scanning.** CLI scaffolder is template-based with text markers in the central file.

## Accepted trade-offs

1. **Components-struct bundle has slight verbosity at adoption.** Adding a component means writing the package + extending the central `Components` struct + wiring + actions registration. The CLI scaffolder does all three.
2. **Refresh round-trip latency.** WS hint → htmx GET → server renders → swap. Adds maybe 30–100ms per refresh vs server-pushed HTML. For cases where this matters, `websocket.NewOuterHTMLEvent` is still available; components don't depend on it.
3. **Ctx-stash uses `e.Set` / `e.Get`** (echo's string map) — not as type-safe as `context.WithValue`. Mitigated by each package owning its own well-known key and providing typed `With` / `Render` helpers.
4. **No framework-enforced authorization on actions.** Apps must wire CSRF, auth, rate-limiting via normal Echo middleware. Identical to how regular hamr handlers work.
5. **Concurrent goroutines complete even if the parent templ skips them.** Wasted work if a component is conditionally not rendered. Buffered channel prevents leaks. Trade-off: simplicity over precise lifecycle.

## Next steps (if approved)

1. Review the design doc.
2. Resolve open questions in §5 (Reactive's hx-get derivation, error fallback, cancellation, CLI patch resilience, multi-dir support).
3. Begin v1 implementation:
   - `pkg/hamr/load.go`
   - `pkg/hamr/reactive.templ`
   - JS bridge in `pkg/websocket/ws.js.tmpl`
   - `hamr add comp <name>` CLI command
   - `hamr.toml` schema bump
4. Update `docs/guide/` and `llmsdocs/llms.txt` + `llms-full.txt` as part of the v1 PR.

No code has been written. This is still an exploratory design proposal.
