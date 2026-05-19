# Codex Critique — Round 6

> Date: 2026-05-15
> Reviewer: codex CLI (gpt-5.5, xhigh)
> Target: design.md iteration 6

## Findings

| # | Severity | Title | Section | Status |
|---|---|---|---|---|
| 1 | **BLOCKER** | Rendered-set drops on WS disconnect → can't validate reconnects | §3.7, §3.10 | NEW |
| 2 | MAJOR | Phase 1 still has rendered set, render IDs, sync, reconciliation | §3.7, §4 | REPEAT |
| 3 | MAJOR | Stateful child props trap (still) | §2.6, §3.3 | REPEAT (accepted) |
| 4 | MAJOR | Nested render cost: guideline doesn't fix the invariant | §2.6, §3.10 | REPEAT (accepted) |
| 5 | MAJOR | Read-only render can observe torn state (no lock on Load) | §3.4, §3.6 | NEW |
| 6 | MAJOR | Action dispatch is public RPC; Authorize easy to omit | §2.4, §3.1, §3.9 | REPEAT (Authorize re-added optional) |
| 7 | MAJOR | CSRF "boot-panics if absent" — mechanism unspecified | §3.9 | NEW |
| 8 | MAJOR | Multi-tab footgun (ScopeSubject = shared across tabs) | §3.8 | REPEAT (accepted) |
| 9 | **BLOCKER** | HA depends on unimplemented `pg_hub.go` or sticky sessions | §3.7, §3.10 | REPEAT (prereq) |
| 10 | MAJOR | Rolling-deploy action-rename brittleness | §3.10 | REPEAT (accepted) |
| 11 | MAJOR | Render ID security contract not stated | §3.7, §3.9 | NEW |
| 12 | MAJOR | OnRefresh idempotency contract too vague | §3.3, §3.7 | NEW |
| 13 | MAJOR | Store interface pushes hard semantics onto Redis implementers | §3.4 | NEW |
| 14 | MAJOR | HX-Refresh blast radius still painful | §3.3, §3.10 | REPEAT (accepted) |
| 15 | MINOR | One-sentence rule still elastic | §1 | REPEAT (accepted as heuristic) |

## Convergence note

Several findings are repeats of already-documented trade-offs (#3, #4, #8, #10, #14, #15). These are not bugs; they are the design's intentional costs. Continuing to "fix" them would mean adding mechanism we've already explicitly rejected (signed snapshots, client-stored state, magic auth interfaces).

The new actionable findings in this round are #1, #5, #7, #11, #12, #13.

## Verdict

"Salvageable, but too much of the complexity has migrated into lifecycle bookkeeping and deployment assumptions."

## Revision plan for iteration 7

| Finding | Action |
|---|---|
| 1. Rendered-set drops on disconnect | Tie rendered-set lifetime to **page-view**, not WS connection. The page-view is identified by a server-issued `renderID` that lives in the session/store for the page's TTL (matches state TTL: 24h). WS reconnects use the same renderID to re-establish subscriptions. |
| 5. Torn state on read | Document Load contract: Load returns a **point-in-time snapshot**. Renderer makes at most one Load per component per render-pass. No cross-component snapshot guarantee (rendering many components in one page does not provide a global snapshot). Apps that need that → DB transaction in the handler before rendering. |
| 7. CSRF mechanism | `component.Group` accepts a typed `CSRFMiddleware` interface marker (or accepts a single `echo.MiddlewareFunc` flagged via a recognised marker function). Boot-panic if no middleware in the list satisfies the marker. Document the explicit signature. |
| 11. Render ID security | Render ID is 128-bit random, subject-bound (server records `renderID → subjectID`), single page-view (one per page render), and matched against subject on every subscribe message. Document this as security-critical. |
| 12. OnRefresh idempotency | Hard contract: OnRefresh MUST be idempotent and side-effect-free except for state mutation. Durable writes, notifications, counters, external API side effects → forbidden in OnRefresh. Use a separate background job for those. Document explicitly. |
| 13. Redis implementation footgun | Narrow the extension point. Phase 2 ships only `MemoryStore` + `PostgresStore`. `RedisStore` is documented as "not in v1 — implement at your own risk; semantics must match the contract". Don't pretend it's a first-class option until we ship a tested one. |
| 9 (HA) | Already documented as prerequisite. Repeat acknowledgement. |
