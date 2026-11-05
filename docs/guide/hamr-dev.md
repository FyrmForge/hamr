# hamr dev

`hamr dev` replaces the rebuild-restart-refresh cycle: it starts your Docker Compose dependencies, rebuilds on every save, restarts the app and reloads the browser. Everything it does is configured by the `[dev]` tables in `hamr.toml`.

**Package references:** [CLI](cli.md), [Dev](pkg/dev.md)

## Starting the dev server

```sh
hamr dev
```

This reads `hamr.toml` from the current directory and starts:

1. Docker Compose dependencies (Postgres, Redis, RustFS, etc.)
2. Initial build (templ generate → Go build → start app)
3. File watchers with automatic rebuild on changes
4. Reverse proxy with live reload injection

Your browser connects to the proxy (`localhost:3000`), which forwards to your app and injects live reload. The scaffold sets `proxy_target = "$PORT"`, so the target follows `PORT` from `.env` (see [`[proxy]`](hamr-toml.md#proxy--reverse-proxy--live-reload)).

```text
Browser :3000  →  hamr proxy  →  App :8080
                      ↓
               SSE /__hamr/reload  (Server-Sent Events)
```

### Flags

```sh
hamr dev --config my.toml            # custom config path
hamr dev --no-proxy                  # skip proxy, just run watchers (API-only projects)
hamr dev --verbose                   # detailed watcher/rebuild logs
hamr dev --headless > dev.log &      # no TUI, plain lines on stdout (agents, CI)
hamr dev --skip-version-check        # bypass the scaffold/CLI version guard
```

On startup, `hamr dev` compares `[hamr].version` in `hamr.toml` against the CLI. If the scaffold is newer than the CLI, it refuses to start — using an older CLI against a newer scaffold risks missing features it depends on. Upgrade the CLI or pass `--skip-version-check` to bypass. When the CLI is *newer* than the scaffold, dev still runs and the status bar flags the mismatch so you can plan an upgrade.

### TUI shell

`hamr dev` runs as a terminal UI: a status bar (active tab, proxy or tunnel URL, MCP state), a scrollable log viewport, and a hint bar with the core keys (`r` rebuild, `o` open, `m` make, `:` commands, `/` search, `?` help, `q` quit). `:` opens a command palette that also lists every Makefile target as `make:<target>`, and `Tab` cycles a log tab per `[[dev.docker_compose]]` entry. `--headless` (automatic when stdout is not a terminal) skips the TUI and writes plain log lines to stdout.

Hotkeys, search, line selection and the status ticker are covered in [CLI → `hamr dev`](cli.md#hamr-dev) and [Dev → Terminal Hotkeys](pkg/dev.md#terminal-hotkeys).

## Watch rules

Each `[[dev.watch]]` block defines what to watch and what to do when files change:

```toml title="hamr.toml"
[[dev.watch]]
name = "templ"
watch = "**/*.templ"
ignore = "*_templ.go"
cmd = "templ generate"
debounce = 100

[[dev.watch]]
name = "site"
watch = ["**/*.go", "**/*.templ", ".env"]
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
| `run` | Long-running process to (re)start after `cmd` succeeds. A rule needs `cmd` or `run` |
| `dir` | Working directory for `cmd`/`run`, relative to the project root. `watch`/`ignore` stay root-relative |
| `env` | `KEY=value` pairs for `cmd` and `run` |
| `depends` | Rules that must complete before this one runs |
| `debounce` | Delay before firing (prevents rapid re-triggers when multiple files change at once) — `100` (ms) or `"200ms"` |
| `reload` | Browser reload: `"full"`, `"css"`, `"none"` (default `"none"`) |

### Dependencies

Rules can depend on other rules. When a `.templ` file changes:

1. `templ` rule runs `templ generate`
2. `site` rule waits (because `depends = ["templ"]`), then builds
3. The app process restarts
4. Browser reloads

### Reload scopes

| Scope | Behavior |
|-------|----------|
| `"full"` | Fetches updated page and swaps `<body>` — no white flash |
| `"css"` | Hot-swaps stylesheets without page reload |
| `"none"` | Rebuild only, no browser reload |

## Daemons

Long-running processes that start once and run for the entire dev session:

```toml title="hamr.toml"
[[dev.daemon]]
name = "sync-static"
cmd = "hamr sync --watch --bucket myapp-static"
```

Daemons are started after the initial build completes.

## Docker Compose

Declare Docker Compose files that hamr ensures are running before builds start:

```toml title="hamr.toml"
[[dev.docker_compose]]
name = "infra"
file = "docker/docker-compose.yaml"
services = ["postgres", "redis"]
keep_running = true
```

`keep_running = true` means the containers stay up when you Ctrl+C hamr, avoiding slow restarts between dev sessions. On the next `hamr dev`, hamr inspects the running stack first via `docker compose ps`: if every expected service is up and ready (healthy, or no healthcheck), hamr **adopts** it — no recreate, no `compose up -d`, no port-walk shift on already-bound ports. If anything is missing, exited, unhealthy, or `starting`, hamr falls through to the apply path and runs `compose up -d` to bring missing services up. Running-but-unhealthy peers are preserved on their current ports (compose up doesn't bounce them) so env injection points consumers at where the container actually is — wipe the stack to force a recreate.

Adoption deliberately does not reconcile config edits against running containers. If you change a base port (or anything else) in the compose file while the stack is still up, the running container keeps its old config and hamr logs a `WARN` per adopted-on-non-base-port service. Wipe the stack from the browser dev panel (or run `docker compose down -v` directly, or wire it into a Makefile target and trigger it from the TUI's `m` palette) to make the edit take effect.

If hamr can't match a running service's published ports to the compose file, it keeps `.hamr/compose.<name>.override.yaml` and logs a `WARN` naming the service. Its port may be missing from `.env` rewrites until you wipe the stack.

## Port walks

When a port hamr manages is busy at startup — proxy listen, the spawned-app `PORT`, or any `[[dev.docker_compose]]` host-published port — `hamr dev` walks +1 up to a small cap and logs a `WARN`. Two `hamr dev` instances on the same machine (or a stray local Postgres on 5432) won't collide.

Values in `.env` that reference walked ports get rewritten transparently for the spawned site, daemons, and any standalone CLI invocation:

- **In-process** (site daemon, `hamr sync` daemon, custom daemons): hamr injects the rewritten `KEY=VALUE` pairs into the child env on top of `.env`. The `.env` file on disk is never modified.
- **Out of process** (`make migrate`, `./scripts/db-shell.sh`, `hamr sync` from the shell): hamr writes `.hamr/walks.json` after walking; `hamr env [--export]` reads it plus `.env` and emits the rewritten pairs. The scaffold's `Makefile` does this automatically via an `ENV_LOAD` prefix on targets that need DB/S3 env. `hamr sync` invoked standalone reads `walks.json` itself — no wrapping needed.

Match rules and the omitted-port limitation are documented in [`hamr.toml` reference → `[dev].port_walk`](hamr-toml.md#port-walks--collision-tolerant-port-binding). Disable with `port_walk = false` in `[dev]` when you need fail-fast behaviour (CI, pinned-port deployments).

## Browser dev panel

The injected live reload script adds a small widget (bottom-left corner) showing:

- **Connection status** — green when connected, red when disconnected
- **Watch rules** — status dots (idle/building/error) with manual trigger buttons
- **Daemons** — running status with command info
- **Docker containers** — per-service health with restart/wipe controls
- **Logs toggle** — opens a terminal-style log window with Hamr and Docker tabs (Docker logs are colour-coded per container, errors highlighted in red)
- **Build errors** — full-page error overlay when a build fails, auto-clears on fix

## File logging

`hamr dev` automatically mirrors its recent output to a rolling log file (default: `.hamr/dev_logs.txt`, 200 lines max). The file contains `[hamr dev]` messages plus stdout/stderr from watched commands and daemons, with terminal escape sequences stripped so LLM tools can read it directly.

Configure in `hamr.toml`:

```toml title="hamr.toml"
[dev]
log_file = ".hamr/dev_logs.txt"      # path (set "none" to disable)
log_file_max_lines = 200             # rolling window size
```

The injected reload script also pipes the browser's `console.*` output, uncaught JS errors, unhandled promise rejections, resource-load failures, and CSP violations back to the dev server over a WebSocket at `/__hamr/console`. They appear in the same TUI tab and `dev_logs.txt` tagged `[site:console]`:

```text
[site:console] hello world
[site:console] WARN deprecated API call
[site:console] ERROR TypeError: x is undefined @ app.js:42:7
[site:console] Failed to load <img> /missing.png
```

Browser-engine warnings printed directly to DevTools (deprecations, mixed-content, autoplay blocked, the formatted "Failed to load resource" lines) and `fetch`/`XHR` network errors aren't observable from page JS, so they aren't captured.

Set `hamr_console_capture = false` in `[dev]` to disable the whole transport — no WS endpoint, no console patching. Default `true`. Independently, set `hamr_console_filter = true` to drop frames whose message contains `[hamr]` (the reload script's own chatter — `[hamr] page swapped`, `[hamr] CSS reloaded`, etc.). Default `false` shows everything.

## Common configurations

### With Tailwind CSS

What `hamr new --css tailwind` writes:

```toml title="hamr.toml"
[[dev.watch]]
name = "tailwind"
watch = ["**/*.templ", "frontend/css/input.css"]
dir = "frontend"
cmd = "npm run css:build"
debounce = 200

[[dev.watch]]
name = "static"
watch = ["frontend/static/**/*"]
cmd = "hamr gen static"
debounce = 200
```

The `tailwind` rule rebuilds `frontend/static/css/output.css` on every `.templ`
change (`dir` runs npm inside `frontend/`; the `watch` globs stay
project-root-relative). The `static` rule re-fingerprints it, which rewrites
`internal/web/components/staticmanifest.go`; that `.go` change triggers the
`site` rule, which rebuilds and does a full reload. A `reload = "css"` rule
doesn't fit here: the page links the fingerprinted filename, which only changes
when the binary is rebuilt.

### API-only project

Leave out the proxy — no `[proxy]` table and none of the `[dev]` proxy keys (`proxy_listen`, `proxy_target`, `inject_reload`), or run `hamr dev --no-proxy`. hamr then just watches, builds, and restarts. The mail, SMS, and Stripe mocks live on the proxy, so enabling them with no proxy configured fails config load.

```toml title="hamr.toml"
[[dev.watch]]
name = "api"
watch = "**/*.go"
cmd = "go build -o bin/api ./cmd/api"
run = "./bin/api"
reload = "none"
```

See [Dev](pkg/dev.md) for the full configuration reference and more examples.
