# Development Workflow

The dev server eliminates the manual rebuild-restart-refresh cycle so you see changes instantly. This guide covers running `hamr dev`, configuring watch rules, and using the browser dev panel.

**Package references:** [CLI](cli.md), [Dev](pkg/dev.md)

---

## Starting the Dev Server

```bash
hamr dev
```

This reads `hamr.toml` from the current directory and starts:

1. Docker Compose dependencies (Postgres, Redis, MinIO, etc.)
2. Initial build (templ generate → Go build → start app)
3. File watchers with automatic rebuild on changes
4. Reverse proxy with live reload injection

Your browser connects to the proxy (`:3000`), which forwards to your app (`:8080`) and injects live reload.

```
Browser :3000  →  hamr proxy  →  App :8080
                      ↓
               SSE /__hamr/reload  (Server-Sent Events)
```

### Flags

```bash
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers (API-only projects)
hamr dev --verbose          # detailed watcher/rebuild logs
```

---

## Watch Rules

Each `[[dev.watch]]` block defines what to watch and what to do when files change:

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
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
depends = ["templ"]
reload = "full"
```

| Field | Description |
|-------|-------------|
| `name` | Unique label (required) |
| `watch` | Glob pattern(s) to match changed files |
| `ignore` | Glob pattern(s) to exclude |
| `cmd` | Build command to run on change |
| `run` | Long-running process to (re)start after `cmd` succeeds |
| `depends` | Rules that must complete before this one runs |
| `debounce` | Delay before firing (prevents rapid re-triggers when multiple files change at once) — `100` (ms) or `"200ms"` |
| `reload` | Browser reload: `"full"`, `"css"`, `"none"` |

### Dependencies

Rules can depend on other rules. When a `.templ` file changes:

1. `templ` rule runs `templ generate`
2. `go` rule waits (because `depends = ["templ"]`), then builds
3. The app process restarts
4. Browser reloads

### Reload Scopes

| Scope | Behavior |
|-------|----------|
| `"full"` | Fetches updated page and swaps `<body>` — no white flash |
| `"css"` | Hot-swaps stylesheets without page reload |
| `"none"` | Rebuild only, no browser reload |

---

## Daemons

Long-running processes that start once and run for the entire dev session:

```toml
[[dev.daemon]]
name = "tailwind"
cmd = "npm run css"

[[dev.daemon]]
name = "sync-static"
cmd = "hamr sync --watch --bucket myapp-static"
```

Daemons are started after the initial build completes.

---

## Docker Compose

Declare Docker Compose files that hamr ensures are running before builds start:

```toml
[[dev.docker_compose]]
name = "infra"
file = "docker/docker-compose.yaml"
services = ["postgres", "redis"]
keep_running = true
```

`keep_running = true` means the containers stay up when you Ctrl+C hamr, avoiding slow restarts between dev sessions.

---

## Browser Dev Panel

The injected live reload script adds a small widget (bottom-left corner) showing:

- **Connection status** — green when connected, red when disconnected
- **Watch rules** — status dots (idle/building/error) with manual trigger buttons
- **Daemons** — running status with command info
- **Docker containers** — per-service health with restart/wipe controls
- **Logs toggle** — opens a terminal-style log window with Hamr and Docker tabs
- **Build errors** — full-page error overlay when a build fails, auto-clears on fix

---

## File Logging

`hamr dev` automatically mirrors its recent output to a rolling log file (default: `.hamr/dev_logs.txt`, 200 lines max). The file contains `[hamr dev]` messages plus stdout/stderr from watched commands and daemons, with terminal escape sequences stripped so LLM tools can read it directly.

Configure in `hamr.toml`:

```toml
[dev]
log_file = ".hamr/dev_logs.txt"      # path (set "none" to disable)
log_file_max_lines = 200             # rolling window size
```

---

## Common Configurations

### With Tailwind CSS

```toml
[[dev.daemon]]
name = "tailwind"
cmd = "npm run css"

[[dev.watch]]
name = "css"
watch = "static/css/output.css"
reload = "css"
```

The Tailwind daemon watches `.templ` files and outputs CSS. The `css` watch rule picks up the output and hot-swaps stylesheets.

### API-Only Project

Omit `[proxy]` entirely — hamr just manages watching, building, and restarting:

```toml
[[dev.watch]]
name = "api"
watch = "**/*.go"
cmd = "go build -o bin/api ./cmd/api"
run = "./bin/api"
reload = "none"
```

See [Dev](pkg/dev.md) for the full configuration reference and more examples.

---

## Next Steps

- [Database](03-database.md) — PostgreSQL connection and migrations
- [Handlers & Routing](04-handlers-routing.md) — Server setup and route groups
