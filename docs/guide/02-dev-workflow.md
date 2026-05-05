# Development Workflow

The dev server eliminates the manual rebuild-restart-refresh cycle so you see changes instantly. This guide covers running `hamr dev`, configuring watch rules, and using the browser dev panel.

**Package references:** [CLI](cli.md), [Dev](pkg/dev.md)

---

## Starting the Dev Server

```bash
hamr dev
```

This reads `hamr.toml` from the current directory and starts:

1. Docker Compose dependencies (Postgres, Redis, RustFS, etc.)
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
hamr dev --config my.toml            # custom config path
hamr dev --no-proxy                  # skip proxy, just run watchers (API-only projects)
hamr dev --verbose                   # detailed watcher/rebuild logs
hamr dev --skip-version-check        # bypass the scaffold/CLI version guard
hamr dev --tui                       # experimental bubbletea TUI shell (preview)
```

On startup, `hamr dev` compares `[hamr].version` in `hamr.toml` against the CLI. If the scaffold is newer than the CLI, it refuses to start — using an older CLI against a newer scaffold risks missing features it depends on. Upgrade the CLI or pass `--skip-version-check` to bypass. When the CLI is *newer* than the scaffold, dev still runs and the status bar flags the mismatch so you can plan an upgrade.

### Experimental TUI mode (`--tui`)

`hamr dev --tui` runs a bubbletea-based shell instead of the legacy stdout output. The dev server itself is identical — same watchers, builds, proxy, mocks — only the terminal UI changes:

- Status bar at the top with build state and any failing rules.
- A scrollable log viewport in the middle (`PgUp`/`PgDn`/arrows to scroll). Long log lines soft-wrap to the viewport width so nothing gets clipped at the right edge; resizing the terminal re-wraps in place.
- Hotkey hints at the bottom.
- Modal overlays for actions that need confirmation.

`q` / `Ctrl+C` work even when the dev server is parked on a `hamr.toml` parse error — the TUI quits cleanly instead of getting stuck on "waiting for config fix...".

Hotkeys mirror the legacy shell, plus a Makefile-target runner, a help overlay, and per-stack log tabs:

| Key | Action |
|-----|--------|
| `r` | Rebuild all watch rules |
| `o` | Open the proxy URL in the browser |
| `c` | Clear the active tab's log buffer |
| `m` | **Run a Makefile target** — opens a fuzzy palette listing every target in the project's `./Makefile` (in declaration order). Type to filter, `↑/↓` to move, `↩` to run, `Esc` to cancel. Hidden when no `Makefile` is present. |
| `Tab` / `Shift+Tab` | Cycle log tabs — `hamr dev` plus one tab per `[[dev.docker_compose]]` entry, fed by `docker compose logs -f` |
| `/` | Search the active tab — incremental: matches highlight in yellow as you type, the first hit jumps into view, `[k/n]` counter updates per keystroke. `↩` locks the query in for navigation, `Esc` cancels. |
| `n` / `N` | Jump to next / previous match (after committing a search). Wraps at the ends. |
| `f` | Toggle filter view (only after committing) — hides every line that doesn't contain the search term. Press `f` again to restore the full view. |
| `Esc` | Clear the active search highlights |
| `?` | Toggle the help overlay (lists every binding) |
| `q` / `Ctrl+C` | Quit |
| `↑` / `↓` | Scroll the log viewport line by line |
| `PgUp` / `PgDn` | Scroll the log viewport by page |
| Mouse wheel | Scroll the log viewport |

The status bar's left side reflects the active tab: 🔨 `hamr dev` for the framework view, 🐳 `<name>` for each docker stack (the `name` from `[[dev.docker_compose]]`). When more than one tab exists the status bar shows `[k/n]` so you know where in the cycle you are.

Search state is **per tab** — committing a query on the docker stack tab and cycling away to hamr keeps both searches alive independently; cycle back and the highlights and `n`/`N` cursor are right where you left them. New log lines arriving while a search is active are scanned and the match counter updates without you having to re-commit.

While a `make` target is running the floating "running" box swallows every key except `q` (cancel — sends `SIGINT` to `make`) and `Ctrl+C` (quits the TUI, taking children with it). Stdout and stderr stream into the hamr tab, prefixed `[make:<target>] ` per line. On exit the box switches to a `Done ✓` / `Failed ✗ (exit N)` summary that stays until you press any key. Define `docker-wipe`, `migrate`, or whatever else you need as Makefile targets and chain them however you like — `m` then becomes the single front door for project-specific scripts.

The TUI is still under active development and runs alongside the legacy shell. Stick with the default unless you want to try the new flow; report rough edges as you find them.

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

`keep_running = true` means the containers stay up when you Ctrl+C hamr, avoiding slow restarts between dev sessions. On the next `hamr dev`, hamr inspects the running stack first via `docker compose ps`: if every expected service is up and ready (healthy, or no healthcheck), hamr **adopts** it — no recreate, no `compose up -d`, no port-walk shift on already-bound ports. If anything is missing, exited, unhealthy, or `starting`, hamr falls through to the apply path and runs `compose up -d` to bring missing services up. Running-but-unhealthy peers are preserved on their current ports (compose up doesn't bounce them) so env injection points consumers at where the container actually is — wipe the stack to force a recreate.

Adoption deliberately does not reconcile config edits against running containers. If you change a base port (or anything else) in the compose file while the stack is still up, the running container keeps its old config and hamr logs a `WARN` per adopted-on-non-base-port service. Wipe the stack from the browser dev panel (or run `docker compose down -v` directly, or wire it into a Makefile target and trigger it from the TUI's `m` palette) to make the edit take effect.

---

## Port Walks

When a port hamr manages is busy at startup — proxy listen, the spawned-app `PORT`, or any `[[dev.docker_compose]]` host-published port — `hamr dev` walks +1 up to a small cap and logs a `WARN`. Two `hamr dev` instances on the same machine (or a stray local Postgres on 5432) won't collide.

Values in `.env` that reference walked ports get rewritten transparently for the spawned site, daemons, and any standalone CLI invocation:

- **In-process** (site daemon, `hamr sync` daemon, custom daemons): hamr injects the rewritten `KEY=VALUE` pairs into the child env on top of `.env`. The `.env` file on disk is never modified.
- **Out of process** (`make migrate`, `./scripts/db-shell.sh`, `hamr sync` from the shell): hamr writes `.hamr/walks.json` after walking; `hamr env [--export]` reads it plus `.env` and emits the rewritten pairs. The scaffold's `Makefile` does this automatically via an `ENV_LOAD` prefix on targets that need DB/S3 env. `hamr sync` invoked standalone reads `walks.json` itself — no wrapping needed.

Match rules and the omitted-port limitation are documented in [`hamr.toml` reference → `[dev].port_walk`](hamr-toml.md). Disable with `port_walk = false` in `[dev]` when you need fail-fast behaviour (CI, pinned-port deployments).

---

## Browser Dev Panel

The injected live reload script adds a small widget (bottom-left corner) showing:

- **Connection status** — green when connected, red when disconnected
- **Watch rules** — status dots (idle/building/error) with manual trigger buttons
- **Daemons** — running status with command info
- **Docker containers** — per-service health with restart/wipe controls
- **Logs toggle** — opens a terminal-style log window with Hamr and Docker tabs (Docker logs are colour-coded per container, errors highlighted in red)
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

The injected reload script also pipes the browser's `console.*` output, uncaught JS errors, unhandled promise rejections, resource-load failures, and CSP violations back to the dev server over a WebSocket at `/__hamr/console`. They appear in the same TUI tab and `dev_logs.txt` tagged `[site:console]`:

```
[site:console] hello world
[site:console] WARN deprecated API call
[site:console] ERROR TypeError: x is undefined @ app.js:42:7
[site:console] Failed to load <img> /missing.png
```

Browser-engine warnings printed directly to DevTools (deprecations, mixed-content, autoplay blocked, the formatted "Failed to load resource" lines) and `fetch`/`XHR` network errors aren't observable from page JS, so they aren't captured.

Set `hamr_console_capture = false` in `[dev]` to disable the whole transport — no WS endpoint, no console patching. Default `true`. Independently, set `hamr_console_filter = true` to drop frames whose message contains `[hamr]` (the reload script's own chatter — `[hamr] page swapped`, `[hamr] CSS reloaded`, etc.). Default `false` shows everything.

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

- [Database](03-database.md) — PostgreSQL or SQLite connection and migrations
- [Handlers & Routing](04-handlers-routing.md) — Server setup and route groups
