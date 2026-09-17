# ADR-004: One event bus for the dev server

- **Status**: Accepted
- **Date**: 2026-09-15
- **Authors**: Dumitru Vulpe

## Context

`hamr dev` has three consumers watching the same running system: the browser
dev panel, the TUI, and an MCP agent. They were wired three different ways.

The browser got a real event stream — `SSEBroker` broadcasts `building`,
`build_ok`, `build_error`, `output`, `reload`, `shutdown` and every tab picks
them up. The TUI got a callback per piece of state: `WithActionsHook`,
`WithProxyURLHook`, `WithMCPStatusHook`, `WithMCPLogHook`. The MCP gateway read
a shared `LogBuffer` the `ProcessManager` fills.

Two concrete failures came out of that split:

**The TUI could not show build state.** `building` / `build_ok` only ever went
to the broker, and the TUI wasn't a subscriber. Adding a "what is the dev
server doing right now" indicator to the status bar meant adding a
`WithBuildStatusHook` — a fourth hook for the third consumer, and another one
for the next piece of state after that.

**TUI-launched make runs were invisible to everything else.** The `m` hotkey
ran `exec.Command("make", target)` inside the TUI and routed the output to its
own viewport. That output never reached the `ProcessManager`, so it never
reached the `LogBuffer` or the broker. An agent calling `logs.read` with
`rule: "make:db-refresh"` got nothing back — while the MCP tool's own
description promised those logs existed. An agent asking "did the migration
run?" was told no by a tool that was simply looking in a place the TUI never
wrote to.

Both are the same defect. A consumer that owns its own wiring sees only what it
wires, and a consumer that runs its own processes is the only one that sees
them.

## Decisions

### The broker is the bus. State goes out on it.

Anything that changes inside the dev server is broadcast on `SSEBroker` as a
typed event. Consumers subscribe: the browser over HTTP via `Handler()`, the
TUI in-process via `Subscribe()`. Both get the same events from the same place.

Each subscriber gets its own 16-slot buffer and a full buffer never blocks the
caller, so a stalled consumer can never wedge a build. That makes the buffer a
shared budget between rare state events and per-line `EvOutput`, and a rebuild
emits output by the hundred, so the naive policy loses whichever event arrives
into a full buffer — including the `build_ok` that clears a build from the
status bar.

Two rules resolve it. `Broadcast` drops `EvOutput` on a full buffer but lets
every other event evict the oldest entry to make room. The asymmetry is about
what a loss costs, not about what is recoverable: a dropped log line costs one
line of scrollback, while a dropped `build_ok` leaves a build showing as
running until something else resets that consumer. Eviction is per client and
serialized by a mutex on the client — `Broadcast` runs under a read lock, so
several goroutines send at once and an unguarded evict-then-retry lets another
sender take the freed slot, losing both events.

On top of that, `Subscribe()` takes event types to exclude, so a consumer
reading output elsewhere need not carry it at all — the TUI passes `EvOutput`,
since process lines reach its viewport through the `ProcessManager` sinks. The
browser panel renders the log overlay and so keeps output; eviction is what
protects it.

New state means a new event constant in `sse.go` and a `case` in each consumer
that cares. It does not mean a new `WithXHook` option.

**Rejected: a hook per state.** It is what we had. Each one is small, which is
why there were four; the cost isn't any single hook, it's that consumers drift
apart until a feature like "show build status in the TUI" needs plumbing that a
different consumer has had for a year.

**Rejected: a new event-bus abstraction with typed subscribers.** The broker
already fans out to N consumers without blocking. An interface layer over it
would be a second way to do the same thing.

### Actions go in through `DevActions`. Consumers don't spawn processes.

Anything a consumer wants *done* goes through `DevActions` — `RebuildAll`,
`RestartServer`, `RunMake`, the docker operations. No consumer runs a command
itself.

This is what fixes the make bug, and it fixes it for every consumer at once:
`DevActions.RunMake` goes through `ProcessManager.RunCommand`, so output lands
in the TUI viewport, the browser log overlay and the shared `LogBuffer`
together, tagged `make:<target>`, whether the run was started by the `m`
hotkey or by an agent's `make.run`.

Access control stays at the edge, not in the shared path: `[dev.mcp]
make_targets` is checked in the MCP tool handler, because it governs what
*agents* may run — pushing it into `RunMake` would silently restrict the
developer's own hotkey to the agent whitelist.

### Typed event constants

`EvBuilding`, `EvMakeStart`, ... in `sse.go`, rather than string literals at
each broadcast. A misspelled literal is not a compile error; it's a consumer
that silently never updates, found weeks later.

## Consequences

- Adding dev-server state is: broadcast a new `Ev*` constant, handle it where
  it matters. No plumbing.
- The TUI status indicator (bottom-right of the hint bar) shows builds, restarts and make runs triggered from
  anywhere — hotkey, browser, or agent.
- `logs.read` on `make:<target>` is now true for every run, as documented.
- Per-target stable colours for `[make:<target>]` tags are gone; make output
  uses the same colour rotation as every other rule. Accepted trade for having
  one output path.
- The `T` tunnel follows the rule: `EvTunnelStart` ("starting"/"stopping"),
  `EvTunnelUp` (URL) and `EvTunnelDown` feed the TUI ticker and URL slot; there
  is no tunnel hook.
- The remaining hooks (`proxyURL`, `mcpStatus`, `mcpLog`, error state) still
  exist. They carry snapshots rather than events, and migrating them is
  follow-up work, not a reason to keep adding more.
