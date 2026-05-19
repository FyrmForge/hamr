# Codex Critique — Round 5

> Date: 2026-05-15
> Reviewer: codex CLI (gpt-5.5, xhigh)
> Target: design.md iteration 5

## Findings

| # | Severity | Title | Section |
|---|---|---|---|
| 1 | **BLOCKER** | Props useless for stateless components (Mount gated on state tag) | §3.3, §2.5 |
| 2 | MAJOR | Phase 1 still has registry + IDs + WS sync + reconciliation — not actually thin | §2.3, §4 |
| 3 | MAJOR | One-sentence rule still over-includes (most forms can claim "transient state") | §1 |
| 4 | **BLOCKER** | Parent→child data flow remains a fundamental composition trap | §2.6, §3.3 |
| 5 | MAJOR | Slot-path identity is brittle app state (refactor strands old state) | §2.5, §3.2, §2.6 |
| 6 | MAJOR | Per-method action auth is too easy to forget | §3.3, §3.9 |
| 7 | MAJOR | Reflection still has rename / stale-tab drift failure modes | §3.1, §4 |
| 8 | MAJOR | Subscription sync trusts client DOM without state-side validation for stateless | §3.7 |
| 9 | MAJOR | `Publish(ctx, bus, topic)` is unclear from background jobs (no subject ctx) | §3.7, §2.7 |
| 10 | MAJOR | Multi-instance story still depends on unimplemented PG WS hub | §3.7, §3.10, §4 |
| 11 | MAJOR | Version mismatch dismissed too aggressively (stale-tab post → 404/refresh) | §3.4, App A |
| 12 | MAJOR | `HX-Refresh` blast radius undermines stateful UX | §3.4, §3.10 |
| 13 | MAJOR | Deep nesting cost makes the abstraction non-local | §2.6, §3.3 |
| 14 | MAJOR | Postgres locking specified but transaction boundaries are not | §3.6, §3.4 |
| 15 | MINOR | DB-as-coordination is intentionally heavy; preserve no-fourth-store but be honest | §2.7 |

## Verdict

"Salvageable but still too eager to become a full UI state runtime." Lifecycle contract (props, identity, parent→child) is still the bottleneck.

## Triage — what's actionable vs already accepted

| # | Action |
|---|---|
| 1. Props for stateless | **Fix.** Call `Mount(ctx, props)` on any `Mounter` regardless of state-tag presence. |
| 2. Phase 1 not thin | **Push back.** Phase 1's minimum IS the framework primitive for routing WS pushes to identified DOM nodes. Document the value prop. A "small helper around WS HTML swaps" without identity/routing/reconciliation doesn't actually deliver the use case. |
| 3. Rule still broad | **Tighten language.** "State that can't sanely live in URL/DB/session" — not "transient state". |
| 4. Parent→child trap | **Already documented at length.** Reaffirm; this is the cost of "no in-DOM state". Patterns A/B exist. |
| 5. Slot-path brittleness | **Already documented.** Refactor-resilience requires either codegen or stored mappings; both rejected. Acknowledge. |
| 6. Per-method auth | **Re-add optional `Authorizer` hook** as an opt-in (received decoded args). Idiomatic method-body checks remain available. Best-of-both. |
| 7. Reflection rename drift | **Document.** Stale tabs posting old action names is true for any URL-based API; mitigation is don't rename action names, version routes for breaking changes. |
| 8. Stateless WS sync validation | **Fix.** Server tracks per-WS-session a "rendered set" of `(name, id)` during page render; subscribe message validates each against that set. For stateful, scope-key load also validates. |
| 9. Publish scope from jobs | **Fix.** Add explicit `PublishToSubject(ctx, bus, subjectID, topic)` for background-job → user-scoped delivery. Document the pattern. |
| 10. Multi-instance dep | **Already documented as prereq.** No change. |
| 11. Stale-tab POST after rename | **Strengthen acknowledgement.** This IS a real failure mode for renames. Mitigation: keep action names stable, use additive changes, accept 404 → reload as the migration cost. |
| 12. `HX-Refresh` blast radius | **Already documented.** 24h TTL makes it rare. |
| 13. Deep nesting cost | **Already documented.** Operator guidance ≤ 3 levels. |
| 14. Transaction boundaries | **Fix.** Store interface gains atomic `Update(ctx, key, fn func([]byte) ([]byte, error), ttl) error` so Load + mutate + Save are guaranteed inside one transaction / advisory-lock scope. |
| 15. DB-as-coordination heavy | **Already accepted.** Acknowledge honestly. |
