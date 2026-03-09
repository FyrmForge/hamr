# Add `depends` to Daemons

## Problem

Watch rules support `depends` for dependency ordering, but daemons have none — they're unconditionally started after all watch rules finish. There's no way to say "start tailwind after sync-static completes" or "start daemon B after daemon A is launched."

## Goal

Docker-compose-style `depends_on` for daemons. Daemons can depend on watch rules and/or other daemons. "Done" means "process started" (matching `depends_on: service_started` semantics).

## Approach

Add daemons as first-class nodes in the dependency graph alongside watch rules. The `Graph` already provides topological ordering and channel-based blocking — just include daemons in it. The two-phase startup (all watch rules, then all daemons) becomes a single topological-order pass interleaving watch-rule builds and daemon starts.

## Changes

### 1. `config.go` — Add `Depends` to `Daemon`

```go
type Daemon struct {
    Name    string        `toml:"name"`
    Cmd     string        `toml:"cmd"`
    Env     []string      `toml:"env"`
    Depends StringOrSlice `toml:"depends"`  // NEW
}
```

- Expand unknown-deps check: use unified `names` map so daemons can depend on watch rules AND other daemons.
- Generalize `detectCycles()`: accept combined name+deps from both watch rules and daemons.

### 2. `graph.go` — Accept daemons in `NewGraph`

```go
func NewGraph(rules []WatchRule, daemons []Daemon) *Graph
```

Add daemon nodes alongside watch rule nodes (same `graphNode` struct).

### 3. `devserver.go` — Unified startup loop

Replace the two-phase startup with a single topological-order loop:

```go
order := graph.TopologicalOrder()
for _, name := range order {
    if rule := r.findRule(name); rule != nil {
        // existing watch rule logic
    } else if daemon := r.findDaemon(name); daemon != nil {
        // start daemon
    }
    graph.MarkDone(name)
}
```

Add `findDaemon(name)` helper.

### 4. Tests

- **config_test.go**: Daemon depends on watch rule, daemon depends on daemon, unknown deps error, cycles involving daemons.
- **graph_test.go**: Update `NewGraph` calls for new signature; add mixed watch+daemon topological ordering test.

## Files

1. `internal/devserver/config.go`
2. `internal/devserver/graph.go`
3. `internal/devserver/devserver.go`
4. `internal/devserver/config_test.go`
5. `internal/devserver/graph_test.go`
