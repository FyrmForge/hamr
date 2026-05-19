# Codex Critique — Round 4

> Date: 2026-05-15
> Reviewer: codex CLI (gpt-5.5, xhigh reasoning)
> Target: design.md iteration 4

## Findings (summarized)

| # | Severity | Title | Section |
|---|---|---|---|
| 1 | **BLOCKER** | Component name not part of ID hash | §3.2 |
| 2 | MAJOR | "Thin runtime" is not thin (framework inside framework) | Decision, §3, §4 |
| 3 | MAJOR | One-sentence rule too broad (reuse is solved by templ partials) | §1 |
| 4 | MAJOR | Props-init-only is a UX trap | §2.7, §3.3 |
| 5 | MAJOR | Slot-path-as-state-invalidation leaks orphan instances | §2.7 Pattern A |
| 6 | MAJOR | `Invoke` couples parent+child too hard, deadlock risk | §2.7 Pattern C |
| 7 | **BLOCKER** | Lock key underspecified | §3.6 |
| 8 | MAJOR | Postgres `hashtext` 32-bit too narrow for advisory lock keys | §3.6 |
| 9 | MAJOR | Phase 3 depends on not-yet-implemented PG WS hub | §3.7, §3.10, §4 |
| 10 | MAJOR | Subscription reconciliation has two conflicting authorities | §3.7 |
| 11 | **BLOCKER** | DOM ID sync lacks name + scope context | §3.7 |
| 12 | MAJOR | CSRF delegated too casually | §3.9 |
| 13 | MAJOR | `Authorize` runs before arg decoding (too coarse) | §3.9 |
| 14 | MAJOR | Version mismatch during rolling deploys = repeated reloads | §3.5, §3.10 |
| 15 | MAJOR | "Mount exactly once" misses auth/perms drift | §3.3, §3.8 |
| 16 | MAJOR | ScopeGlobal footgun (per-user data leaking to global) | §3.8 |
| 17 | MAJOR | Deep nesting → cascade of serialized store ops | §2.7, §3.3, §3.6 |
| 18 | MAJOR | `HX-Refresh` blast radius for stateful-action expiry | §3.10 |
| 19 | MAJOR | Runtime slot-collision panic insufficient for partial renders | §5.3 |
| 20 | MINOR | `Invoke` deferred to Phase 3 but affects core composition | §2.7, §4 |

## The deepest critique (synthesis)

Findings #2 and #3 (and to some extent #4, #5, #6, #17) push on a single point: **the runtime as designed is heavier than the value it delivers.** Reusable templ partials already cover case (a). Stateful interactive widgets are rare in real CRUD apps. Reactivity (case b) is the genuinely new thing, but its operational story (sticky sessions / unimplemented PG hub) is shaky.

This is the central tension to resolve before another correctness pass.

## Things to NOT regress

- Rejecting client-stored signed snapshots.
- Explicit `Actions()` over auto-discovery.
- POST-only actions.
- No shared-state store (URL/DB/topics is enough).
- `OnRefresh` has no HTTP context.
- Multi-tab limitation honest.
- Subscriber as idempotent re-render only.
- Stateless components first phasing.
- Store failure is failure, not zero-state.
- Rejecting `init()` auto-registration.

## Verdict

Salvageable but too heavy. Single most important fix: tighten identity and lifecycle semantics, AND cut surface area aggressively.

## Revision plan for iteration 5 (simplification + correctness)

### Correctness fixes (mandatory)

| Finding | Action |
|---|---|
| 1. Name in ID | `id = base32(sha256(scope + "\x00" + name + "\x00" + slotPath))[:12]`. Name is part of identity. Re-using a slot for a different name = different ID = different state. |
| 7. Lock key spec | Lock key is the full state-store key: `cmp:{scope}:{name}:{id}`. In-memory mutex keyed by this exact string. Postgres advisory lock keyed by SHA-1 of this string (then split into two int32 for `pg_advisory_xact_lock(key1, key2)` — 64-bit space). |
| 8. Advisory lock width | Use the two-arg form: `pg_advisory_xact_lock(int4, int4)`, getting effectively 64-bit lock space from SHA-1 of the state key. |
| 11. DOM sync context | WS subscribe message sends `(name, id)` tuples (not bare DOM IDs). Server validates each against its action registry; rejects unknown. WS scope is bound to the authenticated identity. |
| 12. CSRF explicit | Document explicit CSRF middleware requirement. Phase 2 helper `component.Group(e, mw...)` wraps the route group with CSRF + auth + rate limit by default. App developer cannot accidentally skip. |
| 13. Authorize | Pass already-decoded args to `Authorize`. New signature: `Authorize(ctx context.Context, method string, args any) error`. Runs after arg decode + validate, before method invocation. |

### Simplification cuts (responding to #2, #3, #6, #14, #16, #20)

| Cut | Why |
|---|---|
| **Drop `Invoke` entirely** | Parent→child action coupling creates hidden deps and deadlock risk. Use Pattern B (DB + Publish) instead. |
| **Drop `Versioned`/StateVersion** | Apps own their migration story. JSON tolerance handles additive changes. For breaking changes, app renames the component (= new identity) or accepts a one-time `HX-Refresh` wave during deploy. |
| **Drop `ErrorRenderer`** | Runtime fallback is enough; gold-plating. |
| **Drop TTL keepalive complexity** | Use a 24-hour default TTL for stateful components. Long enough that abandoned tabs eventually clean up, short enough that infinite leak isn't possible. No active keepalive needed. |
| **Drop per-render-pass live-set reconciliation** | V1 accepts ghost subscriptions for at most TTL window. Document. Re-revisit if real apps trip on it. |
| **Drop ScopeGlobal entirely from v1** | Footgun (per #16). All v1 components are ScopeSubject. If you need cross-user state, use raw `pkg/websocket` to push or write to a shared DB record and let users poll/refresh. ScopeGlobal can come back later with explicit isolation guarantees. |
| **Drop component-side `Authorize` requirement** | Auth in middleware. If a component needs per-method or per-arg auth, the method body does the check (with `auth.User(ctx)` available). One less hook to teach. |
| **Drop one-sentence rule's "reuse" case** | Codex #3 is right: templ partials cover reuse. The rule narrows to: "use a component when the server needs to push updates to a fragment without a user action." Components for ONLY reuse is over-engineering. |

### Acknowledged trade-offs (document honestly, no fix)

| Issue | Acknowledgement |
|---|---|
| #4 Props-init-only is sharp | Yes. The only mitigation is the three patterns in §2.7. Document with stronger warnings. |
| #5 Slot-path-encoded data leaves orphans | Accept; TTL cleans up. Document not to use for high-cardinality data. |
| #15 Auth drift after mount | Real but bounded: a user's permissions changing while they have a long-lived stateful component is genuinely unusual. Method body must re-check `auth.User(ctx)` for sensitive operations. Document explicitly. |
| #18 `HX-Refresh` blast radius | Acknowledged in §3.10 already. With longer TTLs (above), it's only the truly-abandoned-tab case. |
| #19 Slot collision detection | Runtime panic at render time is enough; partial renders are detected because the runtime tracks IDs per render-pass. Static lint deferred to post-v1. |
| #17 Deep nesting cost | Mention in operator guide; recommend keeping nesting depth ≤ 3. |
