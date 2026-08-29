# Dev Server — Live Reload Development Environment

## CLI

```bash
hamr dev [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to config file |
| `--no-proxy` | `false` | Skip the reverse proxy, just run watchers |
| `--verbose`, `-v` | `false` | Enable verbose (debug) logging |
| `--headless` | `false` | No TUI: plain log lines on stdout. Automatic when stdout is not a terminal. |

```bash
hamr dev                    # reads hamr.toml from current directory
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers
hamr dev --verbose          # detailed watcher/rebuild logs
hamr dev --headless > dev.log &   # background, e.g. from an AI agent or CI
```

### Headless mode

`--headless` skips the TUI and writes everything — hamr's own lines, rule and
daemon output, `docker compose logs` — to stdout as one stream, so an agent
working in a throwaway worktree can run `hamr dev --headless > dev.log &`,
drive the server through `hamr mcp` / `/__hamr/*` HTTP, and kill it when done.
It switches on by itself whenever stdout is not a terminal (piped or
redirected), so forgetting the flag is harmless. Terminal hotkeys don't exist
in this mode; stop with Ctrl+C / SIGTERM. Output keeps its ANSI colour codes.

---

`hamr dev` runs your entire development stack with a single command: file watching,
build orchestration, process management, Docker Compose dependencies, and a reverse
proxy that injects live reload into every HTML response.

## Configuration

Everything lives in `hamr.toml`. The dev server uses the `[proxy]` and `[dev]` sections.

### Proxy (Optional)

The `[proxy]` section is optional. When present, hamr starts a reverse proxy with
live reload. When omitted, hamr just runs watchers, builds, and processes — useful
for APIs and backend services that don't need browser reload.

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

You can also skip the proxy at runtime with `hamr dev --no-proxy`.

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
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
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
| `dir` | string | Working directory for `cmd`/`run`, relative to the project root (default: project root). Does not affect `watch`/`ignore`. |
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
| `"full"` | Fetch and swap page body without a full browser reload |
| `"css"` | Hot-swap stylesheets without page reload |
| `"none"` | Rebuild only, no browser reload |

### Live Reload

When a `"full"` reload is triggered, the dev server fetches the updated page and
swaps the `<body>` contents via DOMParser — no white flash. Scripts are not
re-executed (they are already loaded); htmx is re-initialized on the new DOM.
The hamr widget, panel, and logs overlay survive the swap. If the fetch fails,
it falls back to `location.reload()`.

`"css"` appends a cache-busting parameter to stylesheet hrefs — no page reload
at all.

### Daemons

Long-running processes that start once and run for the entire dev session:

```toml
[[dev.daemon]]
name = "sync-static"
cmd = "hamr sync --watch --bucket myapp-static"
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique label (required) |
| `cmd` | string | Shell command to run (required) |
| `dir` | string | Working directory for `cmd`, relative to the project root (default: project root) |
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

### Mail Mock

Opt-in, in-memory email inbox viewable at `/__hamr/mail` on the proxy. Apps
send through [`pkg/emailmock`](emailmock.md) (which implements
[`email.Sender`](email.md)) when running locally; real providers are swapped
in for production.

```toml
[dev.email]
enabled           = true                     # opt-in; default false
max_messages      = 500                      # ring-buffer size; oldest evicted first
max_message_bytes = 10485760                 # per-message cap; ingest returns 413 above this
persist           = true                     # default; mirror to mbox file
persist_path      = ".hamr/mail/inbox.mbox"  # default
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Mount the inbox and ingest handler |
| `max_messages` | int | 500 | Ring buffer cap; oldest messages evicted first |
| `max_message_bytes` | int | 10 MiB | Per-message byte cap; over-cap ingests return 413 |
| `persist` | bool | `true` | Mirror inbox to an mbox file so it survives `hamr dev` restart |
| `persist_path` | string | `.hamr/mail/inbox.mbox` | Path to the mbox file |

Requires `[proxy]` to be configured — the inbox UI is served on the proxy mux.
`hamr dev` refuses to start if `enabled = true` without a proxy section.

The inbox survives app restarts triggered by file-watch rebuilds. When
persistence is enabled (default) it also survives restarts of `hamr dev`
itself via the mbox file at `persist_path`. The file is a standard MBOXO
archive — open it in Thunderbird, mutt, or Apple Mail for a real mail-client
view.

See [emailmock](emailmock.md) for usage, magic-recipient failure simulation,
and the HTTP ingest API.

### SMS Mock

Opt-in, in-memory SMS inbox viewable at `/__hamr/sms` on the proxy. Apps send
through [`pkg/smsmock`](smsmock.md) (which implements [`sms.Sender`](sms.md))
when running locally; real providers are swapped in for production.

```toml
[dev.sms]
enabled      = true                     # opt-in; default false
max_messages = 500                      # ring-buffer size; oldest evicted first
persist      = true                     # default; mirror to JSONL file
persist_path = ".hamr/sms/inbox.jsonl"  # default
```

Same proxy requirement and restart-survival behaviour as the mail mock;
persistence uses a JSONL file (one JSON message per line) instead of mbox.
See [smsmock](smsmock.md) for usage, magic-number failure simulation, and the
HTTP ingest API.

## MCP Gateway (AI agents)

With `[dev.mcp].enabled` (or toggled on with `M`), `hamr dev` exposes a
token-gated MCP gateway at `/__hamr/mcp/*` on the proxy. An agent drives it
through the `hamr mcp` stdio bridge:

```
agent  ⟷  hamr mcp  ⟷  hamr dev
        stdio (MCP)      localhost HTTP (/__hamr/mcp/*)
```

The bridge discovers the running dev server and its per-run token from
`.hamr/dev.json` (written by an enabled gateway, mode 0600, gitignored) at call
time — so the agent's config holds no secret and the wiring survives port walks
and restarts.

**Tools** mirror the dev panel's actions, gated by `[dev.mcp.access]`
(per-area `read`/`write`): `dev.info` (discovery), `logs.read`, `console.read`,
`docker.logs`/`status`/`restart`/`wipe`, `rule.run`, `rebuild.all`, `make.run`
(bounded-wait), the mail- and SMS-mock tools, and the stripe-mock lifecycle tools. Reads
return immediately; `docker.restart`/`wipe` and `rule.run` dispatch async (poll
`docker.status` / `logs.read`); `make.run` waits up to `make_wait` then returns
"still running" for slow targets.

Every agent call is recorded three ways: to `.hamr/mcp_logs.txt` (the audit
log), to a dedicated **mcp** TUI tab (last in the `Tab` cycle — one line per
request, reads included), and — for mutations only — tagged `[mcp]` in the main
log tab. The status bar shows the gateway state; `M` is the kill-switch.

Set up an agent with `hamr mcp install` (see [CLI](../cli.md)). Config,
permissions, and the full security model are in
[`[dev.mcp]`](../hamr-toml.md).

## File Logging

By default, `hamr dev` mirrors its recent output to a rolling log file at `.hamr/dev_logs.txt`. The file contains `[hamr dev]` infrastructure messages plus stdout/stderr from watched commands and daemons, with terminal escape sequences stripped. This is designed for LLM consumption: an AI assistant can read the file to understand what happened during the dev session without scraping the terminal.

```toml
[dev]
log_file = ".hamr/dev_logs.txt"     # default path (set to "none" to disable)
log_file_max_lines = 200            # max lines before old entries are pruned
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `log_file` | string | `".hamr/dev_logs.txt"` | Path to the rolling log file. Set to `"none"` to disable. |
| `log_file_max_lines` | int | `200` | Maximum number of lines kept in the file. |
| `hamr_console_capture` | bool | `true` | Pipe browser console + uncaught errors into the dev TUI/log. Set `false` to disable the whole transport. |
| `hamr_console_filter` | bool | `false` | Drop browser-console frames containing `[hamr]` (the reload script's own chatter). Default `false` shows everything. No effect when `hamr_console_capture = false`. |

The `.hamr/` directory is created automatically and is included in the generated `.gitignore`.

### Browser Console Transport

The injected reload script also pipes the browser's `window.console.*` calls, uncaught JS errors, unhandled promise rejections, resource-load failures (`<img>`/`<script>`/`<link>` 404s), and CSP violations back to the dev server over a WebSocket at `/__hamr/console`. They land in the same TUI tab and `dev_logs.txt` tagged `[site:console]`, interleaved with backend events in arrival order:

```
[site:console] hello world
[site:console] WARN deprecated API call
[site:console] ERROR TypeError: x is undefined @ app.js:42:7
[site:console] Failed to load <img> /missing.png
```

Wire format is JSON `{level, msg, src?}` per frame, batched client-side every ~100ms. `level` is one of `log` / `info` / `debug` / `warn` / `error` / `rejection` / `resource` / `csp`. Only `warn` and `error` get a colored level label in the rendered line; `src` is appended as `@ file:line:col` and is populated only for uncaught errors.

Browser-engine warnings printed directly to DevTools (deprecation notices, mixed-content warnings, autoplay blocked, the formatted "Failed to load resource" lines) and `fetch`/`XHR` network errors aren't observable from page JS, so they aren't captured.

Set `hamr_console_capture = false` to disable the entire transport: the server doesn't mount the WS endpoint, the JS reads the flag from the SSE config payload and skips the console patch, the error/rejection/CSP listeners, and the WS connection. Default `true`. Toggling this while a tab is open only takes effect on the next reload — a browser that already started capture keeps reconnect-backoff'ing against the removed endpoint until you refresh.

Independently, set `hamr_console_filter = true` to drop frames whose message *contains* `[hamr]` anywhere in the text — the reload script's own logs (`[hamr] page swapped`, `[hamr] CSS reloaded`, etc.). Substring match is intentional; if app code legitimately logs the literal string `[hamr]` it will be dropped too. Default `false` keeps them; the duplication during normal use is mild and the visibility when hamr's own JS misbehaves is high-value.

## Port Walks

When a port hamr manages is busy at startup — proxy listen, the spawned-app `PORT`, or any `[[dev.docker_compose]]` host-published port — `hamr dev` walks +1 up to a small cap and logs a `WARN` per shift. Default behaviour; turn off with `[dev].port_walk = false` to fail fast on EADDRINUSE.

```toml
[dev]
port_walk = true     # default; +1-on-busy walking with WARN logs
```

After walking, the rewritten port values flow to consumers in two ways:

- **Spawned children** (site daemon, `hamr sync` daemon, custom daemons) get `KEY=VALUE` rewrites injected into their env on top of `.env`. `.env` on disk is never modified.
- **Out-of-process consumers** (`make migrate`, `./scripts/db-shell.sh`, `hamr sync` from the shell) read `.hamr/walks.json`. `hamr env [--export]` prints the rewritten pairs for shell sourcing; the scaffold's `Makefile` wires this in via an `ENV_LOAD := eval "$(hamr env --export)"` prefix.

Match rules and limitations are documented in the [`hamr.toml` reference](../hamr-toml.md). The on-disk `walks.json` schema is the contract between writer and reader; pinned by `internal/devserver/walks_roundtrip_test.go`.

## Lifecycle

```
hamr dev starts
    ↓
docker compose ps             ← inspect each entry's running state
    ↓                           (hard-fail if docker is unavailable)
adopt or apply per entry      ← all expected services running + ready → adopt
                                anything missing/unhealthy → apply
    ↓
docker compose up -d          ← apply path only; skipped on adopt
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

`keep_running = true` plus the adoption path means a stack you left up at the end of the last session is reattached on the next `hamr dev` without recreating containers. Port walking respects this too: a port held by one of the project's own containers is treated as available, so the walk doesn't shift away from a port that's already serving the running stack.

## Browser Dev Panel

The injected script adds a small widget (bottom-left corner) that opens a panel showing:

- **Connection status** — green dot when connected, red pulsing when disconnected
- **Watch rules** — status dots (idle/building/error) with a run button to manually trigger builds
- **Daemons** — status dots (running/error) with command info
- **Docker containers** — per-service health LEDs with a settings menu (restart, wipe & recreate)
- **Logs toggle** — checkbox to open the logs overlay
- **Dark filter** — checkbox that inverts the proxied site (see below)
- **Errors** — build error output when a rule fails

### Dark Filter

A comfort filter for working on a light-mode app: `invert(1) hue-rotate(180deg)`
over the whole document, with images, video, canvas, iframes and hamr's own
overlay re-inverted so they render true. Inline `<svg>` icons flip with the text
around them by design.

Off by default. `[dev].dark_filter = true` in `hamr.toml` (or `.pref.hamr.toml`
for a per-developer preference) sets the initial state; the panel checkbox
`POST`s `/__hamr/dark` to flip it. The state lives in the `hamr dev` process,
is broadcast to every open tab over SSE, dies with the process, and is never
written back to `hamr.toml`.

Caveat: a CSS filter makes `<html>` a containing block, so a site's
`position: fixed` elements anchor to it — rare, but it can shift a fixed layout
while the filter is on.

### Logs Overlay

A terminal-style log window at the bottom of the screen with two tabs:

- **Hamr** — live output from all watch rules, builds, and daemons
- **Docker** — aggregated `docker compose logs` output, colour-coded by container name (error lines highlighted in red)

The visible line count is adjustable and persists across page loads.

## Terminal Status Bar

The TUI renders a status bar at the top with the active tab badge and
health indicators, and a hint bar at the bottom with hotkey reminders.
The 🔨 emoji reflects overall state:

| Color | Meaning |
|-------|---------|
| Green | All builds passing, released CLI, up to date |
| Yellow | Dev build (`DEV`), version mismatch (`VER`), or update available (`UPD`) |
| Red | Active build errors (`ERR rule1, rule2`) |

- **`DEV`** — running a local dev build (not a released binary)
- **`VER cli=X.Y.Z project=A.B.C`** — CLI major.minor differs from the project's `[hamr].version`
- **`UPD latest=X.Y.Z`** — a newer hamr release is available on GitHub (checked once per session)

## Terminal Hotkeys

| Key | Action | Notes |
|-----|--------|-------|
| `r` | Rebuild all watch rules in topological order | |
| `o` | Open the proxy URL in the default browser | Requires `[proxy]` configured |
| `c` | Clear the active tab's log buffer | |
| `m` | Run a Makefile target | Opens a fuzzy palette listing every target in `./Makefile` (declaration order). Type to filter, `↑/↓` to move, `↩` to run, `Esc` to cancel. Hidden when no `Makefile` exists. Output streams to the hamr tab prefixed `[make:<target>]`. While running, only `q` (cancel — kills the `make` process group) and `Ctrl+C` (quit TUI) work. On exit a Done/Failed summary stays until any key dismisses it. |
| `M` | Toggle the MCP gateway | Runtime kill-switch for `[dev.mcp]` — flips the gateway on/off for the session without rewriting `hamr.toml`. The status bar shows `MCP on/<n>` (exposed tool count) or `MCP off`. Shown only when a proxy is running. |
| `Tab` / `Shift+Tab` | Cycle log tabs (hamr → docker stacks → mcp) | One tab per `[[dev.docker_compose]]` entry, fed by `docker compose logs -f --tail=50`; plus a dedicated **mcp** tab (last) when `[dev.mcp]` is configured, showing one line per agent request. |
| `/` | Search the active tab (case-insensitive substring) | Live: highlights and `[k/n]` counter update as you type. `↩` locks in, `Esc` cancels; per-tab persistent. |
| `n` / `N` | Jump to next / previous search match | Wraps at ends. |
| `f` | Toggle filter view (active search only) | Hides every line that doesn't contain the search term; press again to restore. |
| `Esc` | Clear the active search OR selection | If both are live, the first `Esc` drops the selection, a second clears the search. |
| `?` | Toggle the help overlay | Lists every binding including scroll keys. |
| `q` / `Ctrl+C` | Quit gracefully | |
| `↑` / `↓` | Scroll the log viewport line by line | |
| `PgUp` / `PgDn` | Scroll the log viewport by page | |
| Mouse wheel | Scroll the log viewport | |
| Click | Select a log line | Selects the whole logical line, even when soft-wrapped across visual rows. Replaces any previous selection. |
| Click + drag | Extend selection across multiple lines | Press, drag down or up to grow the range. Dragging past the top or bottom edge auto-scrolls (one line per ~50 ms) so you can pick a range larger than the viewport. Stops at the buffer's scroll limits. |
| Shift+Click | Extend selection to clicked line | Inclusive range from the anchor (the most recent plain or range click). |
| Ctrl+Click | Toggle one line in/out of the selection | Anchor follows the click for subsequent shift-clicks. Drag motion under ctrl is ignored — ctrl is a single-line toggle, not a range gesture. |
| `y` | Copy the selected lines to the clipboard | Joined with newlines, ANSI escapes stripped. Selection clears after copy. Clipboard errors surface as a `[hamr:tui] clipboard: ...` line on the hamr tab. |

There is no dedicated wipe hotkey. The browser dev panel still exposes "wipe & recreate" per compose entry (and any HTTP client can hit `DevActions`' `/__hamr/docker/{name}/wipe` route). To wipe from the TUI, define a Makefile target that runs `docker compose -f <file> down -v && docker compose -f <file> up -d` (or whatever recovery you prefer) and trigger it via `m` — the floating "running" box keeps you informed and `q` aborts mid-run.

While at least one log line is selected the bottom hint bar swaps to `[N selected]   y copy   esc clear` and log auto-scroll is paused — the lines you picked stay where they are even as new output streams in. Selection is intentionally **not** per-tab: `Tab` cycling drops it because the stored buffer indices wouldn't line up against the destination tab's buffer. Buffer eviction at the 5000-line cap shifts selection indices in lockstep, dropping anything that scrolls off the start.

### Docker log tabs

When `[[dev.docker_compose]]` entries exist, the TUI spawns one
`docker compose --ansi=always -f <file> logs -f --tail=50` follower per
entry alongside the regular dev pipeline. Output streams into a
per-entry buffer, surfaced as a tab the user cycles through with `Tab`.
The follower self-restarts after a wipe (`down -v` kills the follower;
the next attempt re-attaches once `up -d` brings the project back).
Followers stop when the dev runner shuts down.

`--ansi=always` is set at the compose level so the per-service colour
prefixes survive being piped through a Go writer instead of a TTY. The
bubbles viewport renders the ANSI content unchanged, so any container
output that uses colours (Postgres `LOG:` levels, Redis startup banner,
your app's own logger) shows through.

## Error Handling

### Build Errors

When a build command fails, hamr:
1. Shows a full-page error overlay with the build output
2. Sets a red border on the widget
3. Shows `ERR` with failing rule names in the TUI status bar
4. Continues watching for changes
5. Automatically reloads when the error is fixed

### Config Errors

When `hamr.toml` has a syntax or validation error (at startup or after editing):
1. Logs the error to the terminal
2. Watches the config file for changes
3. Automatically retries when the file is saved

This means you can start `hamr dev` with a broken config, fix it in your editor, and
the dev server picks it up without needing to restart.

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

## Examples

### Standard HAMR Web App

The default `hamr new` setup — Templ + Go + PostgreSQL with live reload:

```toml
[proxy]
listen = ":3000"
target = ":8080"

[[dev.docker_compose]]
name = "infra"
file = "docker/docker-compose.yaml"
keep_running = true

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

### With Tailwind CSS

Add a Tailwind daemon for CSS hot-reloading alongside Templ and Go:

```toml
[proxy]
listen = ":3000"
target = ":8080"

[[dev.watch]]
name = "tailwind"
watch = ["**/*.templ", "frontend/css/input.css"]
dir = "frontend"
cmd = "npm run css:build"
debounce = 200

[[dev.watch]]
name = "templ"
watch = "**/*.templ"
ignore = "*_templ.go"
cmd = "templ generate"
reload = "full"

[[dev.watch]]
name = "css"
watch = "frontend/static/css/output.css"
reload = "css"

[[dev.watch]]
name = "go"
watch = ["**/*.go", "**/*.templ"]
ignore = ["*_templ.go", "*_test.go"]
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
depends = ["templ"]
reload = "full"
```

The Tailwind rule runs `npm run css:build` inside `frontend/`. The `css`
watch rule picks up the output file and hot-swaps stylesheets without a full page reload.

### Web App + API Server

A monorepo with a web frontend (proxied with live reload) and an API service
(auto-rebuilt but not proxied):

```toml
[proxy]
listen = ":3000"
target = ":8080"

[[dev.docker_compose]]
name = "infra"
file = "docker/docker-compose.yaml"

[[dev.watch]]
name = "templ"
watch = "**/*.templ"
cmd = "templ generate"
reload = "full"

# Web server — proxied with live reload
[[dev.watch]]
name = "web"
watch = ["internal/web/**/*.go", "cmd/site/*.go"]
cmd = "go build -o bin/site ./cmd/site"
run = "./bin/site"
depends = ["templ"]
reload = "full"

# API server — rebuilt and restarted on change, no browser reload
[[dev.watch]]
name = "api"
watch = ["internal/api/**/*.go", "cmd/api/*.go"]
cmd = "go build -o bin/api ./cmd/api"
run = "./bin/api"
reload = "none"
env = ["PORT=9090"]
```

The API gets rebuilt and restarted automatically when its Go files change.
`reload = "none"` means no browser reload is triggered and no proxy routing
is involved — it runs independently on its own port.

### API-Only Project

For services that don't serve HTML, omit `[proxy]` entirely. No proxy is started —
hamr just manages watching, building, and restarting:

```toml
[[dev.docker_compose]]
name = "infra"
file = "docker/docker-compose.yaml"

[[dev.watch]]
name = "api"
watch = "**/*.go"
cmd = "go build -o bin/api ./cmd/api"
run = "./bin/api"
reload = "none"
```

### Background Workers

Run multiple workers alongside a web app. Workers are watch rules with
`run` but no browser reload:

```toml
[proxy]
listen = ":3000"
target = ":8080"

[[dev.watch]]
name = "go"
watch = "**/*.go"
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
reload = "full"

[[dev.watch]]
name = "email-worker"
watch = ["internal/worker/**/*.go", "cmd/email-worker/*.go"]
cmd = "go build -o bin/email-worker ./cmd/email-worker"
run = "./bin/email-worker"
reload = "none"

[[dev.watch]]
name = "job-worker"
watch = ["internal/worker/**/*.go", "cmd/job-worker/*.go"]
cmd = "go build -o bin/job-worker ./cmd/job-worker"
run = "./bin/job-worker"
reload = "none"
```

### Static Assets with S3 Sync

Use a daemon to continuously sync static files to S3 during development:

```toml
[proxy]
listen = ":3000"
target = ":8080"

[[dev.daemon]]
name = "sync-static"
cmd = "hamr sync --watch --bucket myapp-static"

[[dev.watch]]
name = "go"
watch = "**/*.go"
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
reload = "full"

[[dev.watch]]
name = "static"
watch = "static/**/*"
reload = "full"
```

### Docker-Heavy Setup

Multiple compose files with selective service management:

```toml
[proxy]
listen = ":3000"
target = ":8080"

# Core infra — keep running between restarts
[[dev.docker_compose]]
name = "infra"
file = "docker/docker-compose.yaml"
services = ["postgres", "redis"]
keep_running = true

# Auxiliary services — stop with hamr
[[dev.docker_compose]]
name = "services"
file = "docker/docker-compose.services.yaml"
services = ["mailhog", "rustfs"]

[[dev.watch]]
name = "go"
watch = "**/*.go"
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
reload = "full"
```

`keep_running = true` on `infra` means Postgres and Redis stay up when you Ctrl+C hamr,
avoiding slow container restarts between dev sessions. Auxiliary services like mailhog
are torn down with hamr since they start quickly.

On the next `hamr dev`, hamr inspects each entry via `docker compose ps`: if every
expected service is running and ready (`healthy` or no healthcheck), the stack is
**adopted** — no `compose up -d`, no recreate, and the host ports already bound by
those containers are excluded from port-walk probing so they aren't shifted off.
Adoption applies to both `keep_running = true` and `keep_running = false` entries —
the flag governs shutdown, adoption governs startup. A service that's missing,
exited, `starting`, or `unhealthy` triggers the apply path for that entry: walk
the missing services (peers stay put), then `compose up -d` to bring them in.
