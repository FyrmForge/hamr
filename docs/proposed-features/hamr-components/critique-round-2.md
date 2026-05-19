# Codex Critique — Round 2

> Date: 2026-05-15
> Reviewer: codex CLI (gpt-5.4)
> Target: design.md iteration 2

## Findings

### 1. 30-bit IDs will collide in real deployments — §3.2 — **BLOCKER**

`base32(sha256(...))[:6]` gives ~30 bits of address space. Birthday collisions become plausible far earlier than this design assumes (sqrt(2^30) ≈ 33k, with a stated 50k subscription cap). A collision aliases state, action routes, and WS push targets between distinct component instances.

### 2. Lifecycle is internally inconsistent about load vs overwrite — §2.2, §2.7, §3.6 — **BLOCKER**

The doc says page render does `factory -> Mount(props) -> save state -> render`, but elsewhere says parent re-renders "load the same state" and registration wiring says the runtime fills `state:"on"` fields from the store on load. Those are different semantics. If page render does not load first, parent re-renders and navigations clobber persisted child state; if it does load first, `Mount` needs a clear contract for merge vs initialize.

### 3. "URL is the source of truth" breaks on websocket-driven refresh — §2.8, §3.6, §3.7 — **BLOCKER**

`OnRefresh` is triggered from a WS subscription registry, not an HTTP request. The design never explains where the current page URL for that specific page-view comes from during a push. The flagship `filter:range` example depends on request context that does not exist in the push path.

### 4. Slot identity collides across pages — §2.6, §3.2, §3.8 — **MAJOR**

Slot uniqueness is only required "within the enclosing page". Two pages using `Embed(..., "header.profile", ...)` for the same component under the same user scope silently hit the same persisted instance. State leaks across routes by string coincidence, not by explicit intent.

### 5. Multi-tab behaviour is fundamentally ambiguous — §2.8, §3.7, §3.8 — **MAJOR**

Removing session/tab scope means "component instance" is "per user + slot string", which is too coarse for interactive UI. Two tabs of the same route share state and produce cross-tab interference that developers will not predict.

### 6. Zero-value state on store failure is corruption disguised as resilience — §3.10 — **MAJOR**

"Store unavailable on Mount → render with zero-value state" is incorrect output that may overwrite good state on the next action or refresh. For many components, zero values are semantically meaningful; users see lies rather than an error.

### 7. `Any("/:name/:id/:method")` contradicts POST-only model — §3.3, §3.9 — **MAJOR**

Documented as `POST /__hamr/components/...` but the route example mounts `Any(...)`. If intentional, CSRF assumptions weaken; if not, two incompatible security models are specified.

### 8. "Later event is dropped" coalescing is too aggressive — §2.5, §3.7 — **MAJOR**

Coalescing assumes `OnRefresh` is idempotent and that reading "latest state" from DB/URL is always enough. False when event sequencing matters, topic identity matters, or intermediate states drive visible behaviour. The runtime silently drops information while exposing an API shape that suggests topic-level semantics are meaningful.

### 9. Phase 3 is not standalone for HA deployments — §3.7, §3.10, §4 — **MINOR**

Reactive components depend on either sticky sessions or an unimplemented cross-instance WS hub. The marquee feature of Phase 3 is operationally incomplete for the common multi-instance case.

### 10. Static `SubscribesTo()` is too rigid — §2.5, §3.1, §3.7 — **MINOR**

Topics cached at registration are type-static, not instance-specific. Fights "reload from URL/DB" once topics need to follow props, record IDs, or route context. Result: over-broad topics and excess fan-out.

## Things to NOT regress

- Killing client-stored signed snapshots is right.
- Required `Actions()` declaration is right.
- No fourth shared-state mechanism is right.
- `HX-Refresh` on expired/missing state is right.
- Central explicit registration is right.
- Avoiding morphdom in v1 is reasonable IF the identity model is fixed.

## Verdict

Salvageable, but only after a hard correction to the component identity model. The most important thing to fix is the definition of what a component instance actually is across page renders, tabs, routes, and WS pushes.

## Revision plan

| Finding | Action |
|---|---|
| 1. 30-bit IDs | Bump suffix to 12+ base32 chars (60+ bits). |
| 2. Mount vs load lifecycle | Explicit semantics: Load first → if hit, use loaded state and skip Mount; if miss, Mount + save. Mount is **initialisation only**, called once per instance lifetime. Props are consumed only at Mount. |
| 3. URL-as-truth breaks on push | Drop URL-based coordination for reactive refresh. Reactive coordination flows through DB. Document: `OnRefresh` has no HTTP request context. |
| 4. Cross-page slot collision | Page handler establishes a slot prefix (default = route path). `Embed` calls nest under it. Explicit, no implicit sharing across routes unless slot is globally rooted. |
| 5. Multi-tab ambiguity | Document explicitly as a known v1 limitation: components are subject-scoped. Tab-divergent state goes in the URL. Future work: optional per-page-view scope via WS-handshake tab token. |
| 6. Zero-state on store failure | Replace with explicit "component unavailable" fallback template. Components may override via `RenderError(ctx, err) templ.Component`. |
| 7. `Any()` route | Change to `POST` only. |
| 8. Coalescing too aggressive | Document `Subscriber`/`OnRefresh` contract: idempotent re-render only. Side-effect-per-event use cases must use raw `pkg/websocket` instead. Coalescing kept for the documented contract. |
| 9. Phase 3 HA dependency | Mark `pkg/websocket/pg_hub.go` (or sticky sessions) as an **explicit prerequisite** in Phase 3, not a footnote. |
| 10. Static `SubscribesTo()` | Make per-instance: called after Load with state populated, returns topics for this specific instance. Registration only validates method shape. |
