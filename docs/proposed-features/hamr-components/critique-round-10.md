# Codex Critique — Round 10 (final)

> Target: design.md iteration 10
> Outcome: declaring convergence after this round.

## Findings (summary)

| # | Severity | Title | Status |
|---|---|---|---|
| 1 | BLOCKER | Stateless WS subscriptions trust client (repeat) | Accepted convention; documented. |
| 2 | MAJOR | Slot paths overloaded (repeat) | Accepted trade. |
| 3 | MAJOR | Page render takes lock + writes for stateful Embed | **Fix in iteration 11**: read-only fast path on Load hit. |
| 4 | MAJOR | Action response ordering can corrupt UI (repeat) | Documented limitation. |
| 5 | MAJOR | PostCommit is dangerous pseudo-outbox | **Fix in iteration 11**: harden guidance — best-effort only, outbox table for durable. |
| 6 | MAJOR | Action transaction boundary incompatible with realistic bodies (repeat) | PostCommit + outbox is the escape valve. |
| 7 | MAJOR | Composition props-init-only (repeat) | Accepted; Patterns A/B/C documented. |
| 8 | MAJOR | Refresh context split (repeat) | Documented in availability table. |
| 9 | MAJOR | Multi-tenant via convention (repeat) | Acknowledged limitation. |
| 10 | MAJOR | HA needs unimplemented PG hub (repeat) | Phase 3 prereq. |
| 11 | MINOR | CSRF API clumsy (repeat) | "Proves presence, not correctness" — best we can do without invasive changes. |
| 12 | MAJOR | Too much surface (repeat) | Phase 2 is opt-in and rare-case. |

## Verdict trend across rounds

| Round | Verdict |
|---|---|
| 1 | salvageable in principle, not in lifecycle |
| 2 | salvageable, not ready |
| 3 | somewhere in between |
| 4 | salvageable but too heavy |
| 5 | somewhere in between, still too eager |
| 6 | salvageable, complexity in lifecycle bookkeeping |
| 7 | salvageable but too stateful |
| 8 | salvageable, not as written |
| 9 | somewhere in between |
| 10 | **somewhere in between** ← 4th consecutive at this verdict |

## Convergence declaration

After 10 iterations and 10 hostile-reviewer passes, the design has reached a stable plateau. The remaining hostile-reviewer findings fall into two categories:

1. **Accepted trade-offs documented in the design.** Stateless authorization via convention, slot-path overloading, multi-tab state sharing, per-tab scope absence, HA prerequisite on `pg_hub.go`, props-init-only composition, refresh-context split — all are explicit costs of deliberate design decisions, with documented mitigations. Adding more mechanism to "fix" them re-introduces the very things we cut (rendered set, signed snapshots, client-stored state).

2. **Genuinely architectural concerns.** "Phase 2 is too much surface for a rare case." That's a strategic question, not a fixable bug. The design's answer: Phase 2 is opt-in; ship Phase 1 first; apps that don't need stateful actions don't pay for them.

Further rounds would re-discover the same concerns without producing actionable improvements.

## Iteration 11 fixes (last)

- Read-only fast-path for stateful page render — no lock when state exists and version matches.
- Hardened PostCommit guidance — best-effort only, durable work goes in outbox table.

The design is now considered final-draft.
