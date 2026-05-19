# Codex Critique — Round 11

> Target: design.md iteration 12 (radical-KISS reset to stateless + typed-Props wrapper)
> Outcome: not-ready. Iteration 13 abandons the registry/Embed/Props-wrapper pattern entirely.

## Findings

| # | Severity | Title | Status |
|---|---|---|---|
| 1 | BLOCKER | Reactive refresh has no way to reconstruct `Props` | Iteration 13: drops server-pushed refresh entirely. Refresh = htmx GET to a per-component action route. |
| 2 | BLOCKER | `Registry.Register[P,T]` is not valid Go (methods cannot declare own type parameters) | Iteration 13: drops registry / Embed / generic wrappers entirely. |
| 3 | MAJOR | Nesting depends on underspecified lazy render state in `Embed` | Iteration 13: no `Embed`. Components are plain templ functions. |
| 4 | MAJOR | Dynamic list slots can update the wrong record (`rows.0` + sorting) | Iteration 13: no slot machinery. |
| 5 | MAJOR | Nested slot parsing is refactor-fragile | Iteration 13: no slot machinery. |
| 6 | MAJOR | Sticky sessions insufficient — publishes happen from non-sticky HTTP handlers too | Inherited concern; `pg_hub.go` required for genuine multi-instance. |
| 7 | MAJOR | Subscribe flood mitigation caps messages, not work per message | Iteration 13: no subscribe protocol; WS reactivity uses existing hamr WS triggers. |
| 8 | MAJOR | Rolling deploy still has a wire ABI via `data-hamr-component` | Iteration 13: no `data-hamr-component`. |
| 9 | MINOR | Error boundaries promised but `Render` has no error return | Iteration 13: error handling is per-component; `hamr.Load` callback returns `templ.Component` (can return an error view). |
| 10 | MINOR | Some observability/failure terms orphaned | Iteration 13: metrics list trimmed to what's relevant. |

## Verdict (codex round 11): not-ready

The reset was directionally simpler, but the missing Props rehydration model and invalid generic registration API were fundamental, not polish.

## Iteration 13 response

After codex, the conversation pivoted hard. Realisations:

1. **Server-pushed HTML refresh is not the hamr way.** hamr's pattern is request/response htmx with WebSockets as pure server-push infrastructure. Server pushing HTML to clients on its own initiative is foreign to the framework.
2. **The Props rehydration BLOCKER dissolves if refresh is htmx-pulled, not server-pushed.** WS sends a topic name; htmx fires a GET; server handles the GET like any other request, with full request context. Props derive from request in both initial render and refresh — no rehydration needed.
3. **Even the htmx-pulled refresh doesn't need a framework registry.** Each component package just exposes a normal Echo handler for its refresh endpoint. Or actions are the things with handlers; refresh-via-action is a normal htmx-post return-HTML pattern. Components themselves are just templ functions.
4. **The whole registry / `Embed[P,T]` / `Register[P,T]` tower was solving problems that exist only in the server-push model.** Cut it. Components are plain `func(e echo.Context, p Props) templ.Component`. Concurrency comes from a tiny goroutine primitive (`hamr.Load`).
5. **Three patterns emerge naturally** — inline templ call, Components-struct bundle, ctx-stash. None require framework infrastructure beyond `hamr.Load`.

Iteration 13 is a near-complete rewrite around these realisations.
