# Codex Critique — Round 3

> Date: 2026-05-15
> Reviewer: codex CLI (gpt-5.4)
> Target: design.md iteration 3

## Findings

### 1. "Mount once, props never again" breaks parent-to-child data flow — §3.3, §2.7 — **BLOCKER**

The runtime ignores props on every load after first mount. A child embedded under a parent cannot reliably reflect new parent-derived inputs once persisted; old state wins forever unless TTL/version expiry forces remount. This is the normal case for nested components, making the claimed composition model false in any tree where parent state drives child behaviour.

### 2. No concurrency control: actions and refreshes lose updates — §3.3, §3.4, §3.7 — **BLOCKER**

Every mutating path is `Load → mutate → Save` on a fresh instance, with no compare-and-swap, version check, or per-component lock. Two rapid clicks, or an action racing a reactive refresh, both load the same old state; whichever Save lands last silently overwrites the other. Broken core invariant.

### 3. Dynamic subscriptions don't stay correct after state changes — §3.7 — **MAJOR**

`SubscribesTo()` is called at page render on the loaded instance, then registered. The design does not say topics are recomputed after an action or `OnRefresh`. A component whose topic depends on state (`orders:user:<id>`) keeps listening to stale topics until a full page render. Missed updates AND ghost updates.

### 4. Subscription lifetime ≠ state lifetime → silent reactivity death — §3.4, §3.7, §3.10 — **MAJOR**

State can expire (30 min) while page and WS are still alive. Publishes hit `ErrNotFound` and skip; widget on screen stops auto-updating without telling the user. "TTL expired between mount and action → `HX-Refresh`" only covers clicks, not idle reactivity.

### 5. ScopeGlobal conflicts with persisted-auth-identity model — §3.4, §3.8 — **BLOCKER**

Envelope stores a single `SubjectID`; reactive refresh reconstructs context from it. For a global component shared across users, whose identity goes in there? Any `OnRefresh` reading user-scoped data from context runs under the wrong principal — correctness AND authorization bug baked in.

### 6. `HX-Refresh` on miss/version mismatch is an ugly global failure mode — §3.3, §3.5, §3.10 — **MAJOR**

A local component action can escalate into a full browser reload. That blows away unrelated in-progress page state and unsaved user input elsewhere on the page. Routine operational events (expiry, restart, rolling deploy) turn into surprising whole-page resets.

### 7. "Sticky sessions are enough" is operationally underspecified — §3.7, §3.10 — **MAJOR**

The local subscription registry only works if initial render, actions, publishes, and WS all land on the same instance stably. "Sticky sessions" does not guarantee that unless deployment couples normal HTTP and WS upgrades with the same affinity key and behaviour across reconnects and rollouts. Risks shipping a feature that works in dev and drops pushes in real HA.

### 8. Removed children leak ghost subscriptions — §3.7, §2.7 — **MAJOR**

Parent re-render replaces child DOM wholesale; components can appear/disappear conditionally. The spec only describes adding subscriptions during render and dropping on WS disconnect. Removed children remain registered and keep receiving pushes for DOM nodes that no longer exist.

### 9. One-sentence rule still not crisp — §1 — **MINOR**

"Reused on more than one page" and "server needs to push updates" are too coarse to steer devs around the lifecycle traps above. A dev can follow the rule and still hit: reused widget with changing props, reactive widget with TTL expiry, nested widget with stale subs.

## Things to NOT regress

- Explicit `Actions()` allow-list.
- No client-stored props / signed snapshots.
- `OnRefresh` has no HTTP request context; reads durable state.
- Store failure is failure, not zero-value fake state.
- Multi-tab limitation is explicit.
- HA reactive push is an explicit prerequisite, not a footnote.

## Verdict

Salvageable, not ready. The single most important fix is the state/identity lifecycle: "load old state and ignore new props" causes composition, auth context, TTL behaviour, and reactive correctness all to collapse around the same bad invariant.

## Revision plan

| Finding | Action |
|---|---|
| 1. Mount-once breaks parent→child | Keep "props are init-only" semantic, BUT add explicit guidance: parent→child data flow goes through (a) slot-path-encoded data, (b) child reads DB on refresh, or (c) parent invokes a child action. Document with worked examples. |
| 2. No concurrency control | Per-component mutex held across Load→Save (action + reactive refresh). PostgresStore uses advisory lock or SELECT FOR UPDATE keyed by component ID. |
| 3. Stale subscriptions after state change | After every state mutation (action result, OnRefresh result), re-call `SubscribesTo()`, diff against previous list, register additions, drop removals. |
| 4. TTL kills reactivity silently | Subscribed components have state TTL refreshed by a WS-hub keepalive (e.g. every 10 min the hub touches all subscribed instances' state rows). State outlives 30 min while WS is alive. |
| 5. ScopeGlobal + envelope identity | Drop SubjectID-in-envelope. Derive identity from scope prefix at OnRefresh time. ScopeGlobal has no user context — document that and forbid auth-context reads in global-scoped components. |
| 6. HX-Refresh as global reset | Acknowledge honestly as a known cost. Fix #4 makes it rare (only abandoned tabs hit it). Document as a deliberate trade for security and simplicity. |
| 7. Sticky sessions underspecified | Spell out the requirements: same affinity key for HTTP and WS, stable across reconnects, behaviour during rollout (brief disconnect, client re-subscribes on reconnect). Operator checklist in §3.10. |
| 8. Ghost subscriptions on removed children | Per-render-pass reconciliation: each render (page or action returning HTML) emits a "live set" of mounted IDs. Runtime diffs against previously-registered IDs for the same WS session + root, unregisters dropped. |
| 9. One-sentence rule still coarse | Keep the rule but add a "before adopting, read §2.7 + §3.3" pointer. The rule is a heuristic, not a substitute for understanding the lifecycle. |
