# Codex Critique — Round 9

> Target: design.md iteration 9

## Findings

| # | Severity | Title | Action |
|---|---|---|---|
| 1 | BLOCKER | Stateless subscriptions trust client too much (repeat) | Accepted limitation; reaffirm with `MustScope` guidance. |
| 2 | BLOCKER | Sticky sessions don't solve cross-instance publish (repeat) | Documented Phase 1 prerequisite. |
| 3 | MAJOR | Reconnect misses events fired while disconnected | **Fix:** post-subscribe, server pushes current render for each subscribed component. |
| 4 | MAJOR | Action response rendering not serialized; out-of-order swaps | Document as known limitation; htmx history sequencing not in v1. |
| 5 | MAJOR | Publish from inside actions deadlock/order hazard | **Fix:** `Publish` inside an action is deferred until after `Update` commits. |
| 6 | MAJOR | Action contract forbids work actions usually do | **Fix:** `component.PostCommit(ctx, fn)` hook for outbox-style after-commit work. |
| 7 | MAJOR | No per-tab scope breaks wizard example (repeat) | Accept; soften wizard example by suggesting URL-step encoding. |
| 8 | MAJOR | Slot paths overloaded (repeat) | Accepted trade. |
| 9 | BLOCKER | CSRF marker design isn't sound Go (`echo.MiddlewareFunc` is a function type) | **Fix:** redesign marker as a wrapper type. |
| 10 | MAJOR | WS-pushed component swaps don't trigger resync | **Fix:** inline JS listens for hamr WS HTML-swap events and re-runs the sync after each. |
| 11 | MAJOR | Render context split between HTTP and refresh paths | Document context-availability table explicitly. |
| 12 | MINOR | One-sentence rule too subjective | Accepted heuristic. |

## Stop point

After iteration 10 ships these fixes, design is at a stable plateau. Verdict has been "somewhere in between" for three consecutive rounds (7, 8, 9). The remaining concerns are documented trade-offs the design deliberately accepts (multi-tab, slot brittleness, HA prereq, props init-only, stateless convention security). Further rounds would re-discover them without producing actionable improvements.
