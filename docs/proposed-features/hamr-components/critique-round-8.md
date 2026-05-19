# Codex Critique — Round 8

> Target: design.md iteration 8

## Findings

| # | Severity | Title | Action |
|---|---|---|---|
| 1 | MAJOR | Phase 2 not justified by the rule | Strategic; documented as opt-in rare case. Keep. |
| 2 | **BLOCKER** | Action requests can't reconstruct slot | Fix: store slot in state envelope; restore to ctx on load. |
| 3 | **BLOCKER** | Stateless WS subscribe is auth footgun (convention-only) | Fix: document the hard rule + provide a `scope` query helper. |
| 4 | **BLOCKER** | Stateful reconnect SubscribesTo runs on zero-value state | Fix: subscribe processing for stateful must load state first. |
| 5 | MAJOR | Subscription lifecycle contradicts itself across §3.3 / §3.7 / §4 | Fix: single source of truth — client DOM is authoritative; server doesn't pre-register on render. |
| 6 | MAJOR | Coalescing can drop the final update | Fix: trailing-render flag; after in-flight completes, if dirty, render once more. |
| 7 | MAJOR | Sticky sessions don't solve cross-instance publish | Already documented as prereq; restate. |
| 8 | MAJOR | `Update` runs user code inside lock — side effects can't roll back | Fix: hard rule — action methods are brief, no external side effects inside Update. Side effects → background jobs. |
| 9 | MAJOR | Nested render can overwrite newer child DOM | Accepted; document as known limitation. |
| 10 | MAJOR | No per-tab scope dangerous for stateful | Accepted; documented. |
| 11 | MAJOR | Slot paths overloaded | Accepted consequence of no rendered set. |
| 12 | MAJOR | Duplicate slot detection cannot be deferred | Fix: per-render-pass ID tracking; runtime panic on duplicate. |
| 13 | MAJOR | Rolling deploy state compat overstated | Fix: re-add optional `StateVersion()` interface; default V1; mismatch → `ErrNotFound` → `HX-Refresh`. |

## Convergence

After this round (iteration 9), I will stop iterating. The remaining repeats are documented trade-offs — every further round will find them anew because they are the design's deliberate costs. Continuing would chase tail ends rather than land the design.
