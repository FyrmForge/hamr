# Codex Critique — Round 1

> Date: 2026-05-15
> Reviewer: codex CLI (gpt-5.4)
> Target: design.md initial draft

## Findings

### 1. Child identity is underspecified and likely broken across parent re-renders — §2.7, §3.2 — **BLOCKER**

The design claims that `Embed` on parent re-render "reloads child state from the store" so children survive, but the ID scheme does not explain how the same child nonce is recovered on a later render. IDs are random, the cache is only "render-scoped," and the lookup key mentions a "nonce-from-render-context" that is never defined for subsequent requests. If the child gets a new ID, its stored state, subscriptions, and action URLs all fork; if the runtime tries to infer identity from `(name, props-hash)`, identical siblings collide.

### 2. Silent re-mount from client-stored props turns expiry/restart into a tampering path — §3.6 — **BLOCKER**

On missing state, the server rebuilds the component from `data-hamr-props` stored in the DOM, and those props are explicitly unsigned. That means state expiry or a restart changes the trust boundary: values that were originally server-chosen become client-editable remount inputs. "Props are inputs the page already chose" is not a safety argument; if a prop carries object IDs, scope selectors, or authorization-relevant context, an attacker gets a second, weaker code path that bypasses the original page render.

### 3. "Any exported method" as an action surface is a serious footgun — §3.3, §3.9 — **MAJOR**

This design makes remote invocability an ambient property of Go visibility and signature shape, not an explicit declaration. That is exactly the kind of rule developers forget when refactoring: a helper like `Delete`, `Refresh`, `Reindex`, or `Sync` can silently become a POST endpoint because it happens to fit `func(ctx) error`. The optional `Actions() []string` allow-list does not fix the default hazard; it just adds a second, safer mode that teams must remember to opt into.

### 4. The "session = per tab" model does not line up with HTTP action requests — §2.8, §3.4, §3.9, §5.5 — **BLOCKER**

The doc repeatedly treats `ScopeSession` as "roughly per tab" via the websocket hub model, but component actions are plain HTTP POSTs. HTTP requests do not naturally carry a per-tab identity; cookies are per browser session, not per tab, and the design does not specify a trustworthy tab-scoped token beyond the component ID nonce, which it also says is not auth. Without a concrete binding, either "session scope" collapses to browser-wide scope, or the server must trust extra client-provided scope material and reintroduce exactly the token-auth problem the design says it avoids.

### 5. Reactive subscriptions will leak and do useless work for dead page trees — §3.6, §3.7, §5.5 — **MAJOR**

Unmount is effectively "wait for TTL," so every navigation leaves mounted subscribers behind for up to 30 minutes. During that window, publishes still fan out DB loads, renders, and websocket pushes for components no user can see anymore. This is not just inefficiency; it makes topic cost proportional to historical page views rather than current live UI, which is a bad operational invariant for a server-push feature.

### 6. The "concurrent loaders" justification does not carry the weight of a new subsystem — §1, §2.6, §4.1 — **MAJOR**

Case (c) is doing too much rhetorical work. HAMR can already do concurrent server work in handlers without introducing a registry, ID lifecycle, store semantics, action routing model, and future reactive baggage just to render ten tiles. If Phase 1's main win is parallel `Mount`, then Phase 1 is mostly a templ-function replacement with more machinery, which cuts directly against the document's stated bias toward avoiding overhead.

### 7. Shared state is a fourth state channel with implicit coupling and weak debugging properties — §2.8, §3.8 — **MAJOR**

The doc argues SharedGet/SharedSet is distinct from URL, session, and DB, but in practice it is another persistence and invalidation mechanism keyed by ad hoc strings. Reads are declared separately from writes, updates happen by side-effecting auto-publish topics, and components re-render because some other component touched `"shared:{scope}:{key}"`. That creates invisible cross-component dependencies that will be harder to reason about than just using URL or DB state, especially once a page mixes all four mechanisms.

### 8. Publish fan-out can create a thundering herd on hot topics — §2.5, §3.7, §3.10 — **MAJOR**

A single topic publish can trigger N state loads, N refreshes, N renders, and N websocket pushes, all concurrently up to a cap, with no coalescing or deduping. For subject/global topics on busy pages, that is an application-level amplification mechanism tied directly to event frequency. The design assumes "re-render and reread source of truth" is the safe default, but operationally that default can turn one backend event into a burst of repeated DB work and HTML generation.

## Things the doc gets right and should NOT regress

- Rejecting signed DOM snapshots is the right simplification.
- Central explicit registration is better than `init()` magic or AST scanning.
- Boot-time validation instead of request-time surprise is the correct failure mode.
- Reusing normal htmx form posts and HTML responses is the right instinct; a new client runtime would be a regression.
- Treating Phase 4 shared state as optional and demand-gated is correct; it should not be assumed necessary.
- Calling out the no-morphdom trade-off explicitly is good.

## Verdict

Salvageable in principle, but not in its current lifecycle model. The single most important thing to fix is **component identity and trust across requests**: until child IDs persist correctly, remount inputs are trustworthy, and scope is bound concretely for HTTP actions, the subsystem's core invariants do not hold.

## My response plan (revision intent)

| Finding | Action |
|---|---|
| 1. Child identity broken | Drop random nonces. `Embed` requires explicit `slot` string. IDs are deterministic hashes of `(scope-prefix, slot-path)`. |
| 2. Unsigned props re-mount = tampering | Drop silent re-mount entirely. Drop `data-hamr-props`. Expiry → `HX-Refresh: true` and a fresh page render. |
| 3. Exported-method footgun | Require explicit `Actions() []string` declaration. No auto-discovery. Boot-time validates every entry resolves to a valid-signature method. |
| 4. ScopeSession doesn't fit HTTP | Drop ScopeSession entirely. Only ScopeSubject and ScopeGlobal. Per-tab state goes in the URL. |
| 5. Subscription leak | Subscriptions tied to WS connection lifetime, not state TTL. WS disconnect → drop subs. Re-mount on next page render re-registers. |
| 6. "Concurrent loaders" doesn't justify the subsystem | Drop case (c) from the rule. Components are for reuse OR server-push. Concurrent fan-out is a side benefit, not a marketed win. |
| 7. Shared state is a fourth channel | Remove SharedGet/SharedSet entirely. Inter-component coordination = URL, DB, or pub/sub topics. No fourth mechanism. |
| 8. Publish fan-out herd | Per-component in-flight coalescing (one render per component-id at a time). Per-topic debounce config. Document hot-topic guidance. |
