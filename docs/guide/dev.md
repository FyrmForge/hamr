# Dev Server — Live Reload Development Environment

`hamr dev` runs your entire development stack with a single command: file watching,
build orchestration, process management, Docker Compose dependencies, and a reverse
proxy that injects live reload into every HTML response.

## Quick Start

```bash
hamr dev                    # reads hamr.toml from current directory
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers
hamr dev --verbose          # detailed watcher/rebuild logs
```

## Configuration

Everything lives in `hamr.toml`. The dev server uses the `[proxy]` and `[dev]` sections.

### Proxy

```toml
[proxy]
listen = ":3000"        # dev proxy listens here (default: ":3000")
target = ":8080"        # your app listens here (default: ":8080")
inject_reload = true    # inject live reload script into HTML (default: true)
```

The proxy sits between your browser and your app:

```
Browser :3000  →  hamr proxy  →  App :8080
                      ↓
               SSE /__hamr/reload
```

All HTML responses get a small script injected before `</body>` that opens an SSE
connection. When builds complete, the browser reloads automatically.

### Watch Rules

Each `[[dev.watch]]` block defines a file pattern to watch and an action to take:

```toml
[[dev.watch]]
name = "templ"
watch = "**/*.templ"
ignore = "*_templ.go"
cmd = "templ generate"
debounce = 100

[[dev.watch]]
name = "go"
watch = ["**/*.go", "**/*.templ"]
ignore = ["*_templ.go", "*_test.go"]
cmd = "go build -o ./bin/app ./cmd/server"
run = "./bin/app"
depends = ["templ"]
reload = "full"
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique label for this rule (required) |
| `watch` | string or list | Glob pattern(s) to match changed files (required) |
| `ignore` | string or list | Glob pattern(s) to exclude |
| `cmd` | string | Shell command to run on change |
| `run` | string | Long-running process to (re)start after `cmd` succeeds |
| `depends` | string or list | Rules that must complete before this one runs |
| `debounce` | int or string | Delay before firing — `100` (ms) or `"200ms"` (default: 100ms) |
| `reload` | string or bool | Browser reload scope: `"full"`, `"css"`, `"none"`, or `true`/`false` |
| `env` | list | Extra environment variables: `["KEY=value"]` |

### Dependencies

Rules can depend on other rules. When a `.templ` file changes:

1. `templ` rule runs `templ generate`
2. `go` rule waits (because `depends = ["templ"]`), then builds
3. The app process restarts
4. Browser reloads

Circular dependencies are detected at startup.

### Reload Scopes

| Scope | Behavior |
|-------|----------|
| `"full"` | Full page reload (or DOM morph if idiomorph is available) |
| `"css"` | Hot-swap stylesheets without page reload |
| `"none"` | Rebuild only, no browser reload |

### Daemons

Long-running processes that start once and run for the entire dev session:

```toml
[[dev.daemon]]
name = "tailwind"
cmd = "npm run css"

[[dev.daemon]]
name = "sync-static"
cmd = "hamr sync --watch --bucket myapp-static"
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique label (required) |
| `cmd` | string | Shell command to run (required) |
| `env` | list | Extra environment variables |

Daemons are started after the initial build completes.

### Docker Compose

Declare Docker Compose files that hamr ensures are running before builds start:

```toml
[[dev.docker_compose]]
name = "infra"
file = "docker-compose.yaml"
services = ["postgres", "redis"]
keep_running = true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | — | Unique label (required) |
| `file` | string | — | Path to compose file (required) |
| `services` | list | all | Specific services to manage (omit for all) |
| `keep_running` | bool | `false` | Don't `docker compose down` when hamr stops |
| `env` | list | — | Extra environment variables |

## Lifecycle

```
hamr dev starts
    ↓
docker compose up -d          ← ensure containers running
    ↓
initial build (topological order)
    ↓
start long-running processes (run)
start daemons
    ↓
file watcher active           ← rebuild on changes, reload browser
    ↓
Ctrl+C
    ↓
shutdown event → browser notified
stop processes and daemons
docker compose down           ← unless keep_running = true
```

## Browser Dev Panel

The injected script adds a small widget (bottom-left corner) that opens a panel showing:

- **Connection status** — green dot when connected, red pulsing when disconnected
- **Watch rules** — status dots (idle/building/error) with a run button to manually trigger builds
- **Daemons** — status dots (running/error) with command info
- **Docker containers** — per-service health LEDs with a settings menu (restart, wipe & recreate)
- **Logs toggle** — checkbox to open the logs overlay
- **Errors** — build error output when a rule fails

### Logs Overlay

A terminal-style log window at the bottom of the screen with two tabs:

- **Hamr** — live output from all watch rules, builds, and daemons
- **Docker** — aggregated `docker compose logs` output

The visible line count is adjustable and persists across page loads.

## Error Handling

### Build Errors

When a build command fails, hamr:
1. Shows a full-page error overlay with the build output
2. Sets a red border on the widget
3. Continues watching for changes
4. Automatically reloads when the error is fixed

### Backend Down

When the proxied backend is not responding (during initial build or after a crash):
1. Shows a "Waiting for Server" splash page with the target address
2. Periodically probes the backend
3. Automatically reloads when the backend comes up

Both pages include the live reload script, so they recover automatically.

### Shutdown

When hamr stops (Ctrl+C), it broadcasts a shutdown event to the browser. The browser
stops showing error states and begins reconnecting. When hamr restarts, the UI
reconnects automatically.

## Full Example

```toml
[proxy]
listen = ":3000"
target = ":8080"
inject_reload = true

[[dev.docker_compose]]
name = "infra"
file = "docker-compose.yaml"
keep_running = true

[[dev.watch]]
name = "templ"
watch = "**/*.templ"
ignore = "*_templ.go"
cmd = "templ generate"
debounce = 100

[[dev.daemon]]
name = "tailwind"
cmd = "npm run css"

[[dev.watch]]
name = "go"
watch = ["**/*.go", "**/*.templ"]
ignore = ["*_templ.go", "*_test.go"]
cmd = "go build -o ./bin/app ./cmd/server"
run = "./bin/app"
depends = ["templ"]
reload = "full"
```
