# Codex Critique — Round 7

> Date: 2026-05-15
> Reviewer: codex CLI (gpt-5.5, xhigh)
> Target: design.md iteration 7

## Findings

| # | Severity | Title | Status |
|---|---|---|---|
| 1 | **BLOCKER** | Phase 1 is not actually stateless (24h server-side rendered set) | NEW |
| 2 | **BLOCKER** | Rendered-set backend underspecified in multi-instance | NEW |
| 3 | MAJOR | Props snapshot is a hidden fourth state channel | NEW |
| 4 | MAJOR | One-sentence rule still mushy | REPEAT (accepted) |
| 5 | MAJOR | Mount-once props break composition expectations | REPEAT (accepted) |
| 6 | MAJOR | Slot path as identity is too fragile | REPEAT (accepted) |
| 7 | MAJOR | Authorize ambiguity (optional + idiomatic both available) | REPEAT (accepted) |
| 8 | MAJOR | CSRF marker proves presence not protection | NEW (limited fix possible) |
| 9 | MAJOR | Reactive refresh has many loss modes (drops, coalesce, etc) | REPEAT (accepted; doc as invalidation) |
| 10 | **BLOCKER** | Multi-instance HA depends on TODO | REPEAT (prereq) |
| 11 | MAJOR | Rolling deploy compatibility too weak | REPEAT (accepted) |
| 12 | MAJOR | Deep nesting cost understated | REPEAT (accepted) |
| 13 | MINOR | `PublishToSubject` assumes simple tenancy | NEW (document) |
| 14 | MINOR | Page renders can show inconsistent cross-component state | REPEAT (accepted) |

## The structural finding

Findings 1, 2, 3 are the same root issue: by giving stateless components Mount-with-props in iteration 6, I introduced a server-side rendered-set as the persistence mechanism for those props. That rendered-set IS state — with TTL, security, HA, and cleanup concerns — contradicting the "stateless reactive" framing.

## Revision plan for iteration 8

### Remove the rendered set entirely

Phase 1 stateless components have **no Mount, no props**. All per-instance data must come from one of:
- The slot path (parsed by the component).
- The request context (subject, scope, etc).
- DB / external source-of-truth (read in `Render` or `OnRefresh`).

Mount is exclusively a Phase 2 feature for stateful components, where the resulting state is persisted via `state:"on"` fields.

This kills BLOCKER #1, BLOCKER #2, and MAJOR #3 in one move.

### Slot-based per-instance data API

The runtime exposes a small helper:

```go
slot := component.Slot(ctx)
slot.Raw()             // "accounts.42.tile"
slot.Segment(0)        // "accounts"
slot.Segment(1)        // "42"
slot.Tail()            // "42.tile"
```

Embedding with structured data goes into the slot path:

```templ
@component.Embed(ctx, "account-tile", "accounts." + acc.ID + ".tile", nil)
```

`Embed`'s fourth arg (`Props`) is **only consumed by Phase 2 stateful components on first Mount.** For Phase 1, it's ignored.

### WS subscribe protocol

Stateless: client sends `(name, slot)`. Server validates `subjectID` from the WS session, computes the deterministic ID, registers subscription under `(subject, name, id) → topics`. No rendered-set lookup.

Stateful: client sends `(name, id)`. Server validates that a state record exists for the WS session's subject + name + id.

The slot path is now visible in the rendered DOM as `data-hamr-slot="accounts.42.tile"`. The client reads it from the component root and includes in the subscribe message.

### Other small fixes

| Finding | Action |
|---|---|
| 8. CSRF marker | Acknowledge limitation honestly: marker proves presence, not behaviour. The mitigation is documenting that the marker should only be set by middleware that has been audited. |
| 13. Multi-tenancy | Add a note that subject-only scope is sufficient for single-tenant apps; multi-tenant apps must include tenant info in slot paths or use raw `pkg/websocket` for cross-tenant pushes. |

## Verdict

Salvageable, still too operationally heavy. Single most important fix: resolve the rendered-set / stateless tension (above).

## Stop condition

After iteration 8 ships these fixes, I'll declare the design at a stable plateau. The remaining concerns (multi-tab, deep nesting, rolling deploy brittleness, HA prerequisite, props init-only) are documented trade-offs the design has deliberately accepted, not unaddressed bugs. Continuing rounds would re-discover them rather than find new issues.
