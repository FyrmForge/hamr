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

```bash
hamr dev                    # reads hamr.toml from current directory
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers
hamr dev --verbose          # detailed watcher/rebuild logs
```

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

The `.hamr/` directory is created automatically and is included in the generated `.gitignore`.

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
- **Docker** — aggregated `docker compose logs` output, colour-coded by container name (error lines highlighted in red)

The visible line count is adjustable and persists across page loads.

## Terminal Status Bar

The bottom row of the terminal shows a persistent status bar with hotkey hints and
health indicators. The 🔨 emoji reflects overall state:

| Color | Meaning |
|-------|---------|
| Green | All builds passing, released CLI, up to date |
| Yellow | Dev build (`DEV`), version mismatch (`VER`), or update available (`UPD`) |
| Red | Active build errors (`ERR rule1, rule2`) |

- **`DEV`** — running a local dev build (not a released binary)
- **`VER cli=X.Y.Z project=A.B.C`** — CLI major.minor differs from the project's `[hamr].version`
- **`UPD latest=X.Y.Z`** — a newer hamr release is available on GitHub (checked once per session)

## Terminal Hotkeys

Both the default shell and `--tui` mode bind the same keys for common actions:

| Key | Action | Notes |
|-----|--------|-------|
| `r` | Rebuild all watch rules in topological order | |
| `o` | Open the proxy URL in the default browser | Requires `[proxy]` configured |
| `c` | Clear the active tab's log buffer (TUI) / terminal (legacy) | |
| `d` | Wipe a docker compose stack (`down -v` then `up -d`) | **TUI mode only.** Single-entry stacks confirm directly; multi-entry shows a numbered modal first. Removes volumes — confirm prompt prevents accidents. |
| `Tab` / `Shift+Tab` | Cycle log tabs (hamr → docker stacks → ...) | **TUI mode only.** One tab per `[[dev.docker_compose]]` entry, fed by `docker compose logs -f --tail=50`. |
| `/` | Search the active tab (case-insensitive substring) | **TUI mode only.** Live: highlights and `[k/n]` counter update as you type. `↩` locks in, `Esc` cancels; per-tab persistent. |
| `n` / `N` | Jump to next / previous search match | **TUI mode only.** Wraps at ends. |
| `f` | Toggle filter view (active search only) | **TUI mode only.** Hides every line that doesn't contain the search term; press again to restore. |
| `Esc` | Clear the active search | **TUI mode only.** |
| `?` | Toggle the help overlay | **TUI mode only.** Lists every binding including scroll keys. |
| `q` / `Ctrl+C` | Quit gracefully | |
| `↑` / `↓` | Scroll the log viewport line by line | **TUI mode only.** |
| `PgUp` / `PgDn` | Scroll the log viewport by page | **TUI mode only.** |
| Mouse wheel | Scroll the log viewport | **TUI mode only.** |

The `d` hotkey is the one-key equivalent of the dev panel's "wipe & recreate" button on a docker compose entry. It runs the same path (`DevActions.DockerWipe`), so the dev panel and the TUI hotkey stay in sync.

### Docker log tabs (TUI)

When `[[dev.docker_compose]]` entries exist, the TUI spawns one
`docker compose --ansi=always -f <file> logs -f --tail=50` follower per
entry alongside the regular dev pipeline. Output streams into a
per-entry buffer, surfaced as a tab the user cycles through with `Tab`.
The follower self-restarts after a wipe (`down -v` from `d` kills the
follower; the next attempt re-attaches once `up -d` brings the project
back). Followers stop when the dev runner shuts down.

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
3. Shows `ERR` with failing rule names in the terminal status bar
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

[[dev.daemon]]
name = "tailwind"
cmd = "npm run css"

[[dev.watch]]
name = "templ"
watch = "**/*.templ"
ignore = "*_templ.go"
cmd = "templ generate"
reload = "full"

[[dev.watch]]
name = "css"
watch = "static/css/output.css"
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

The Tailwind daemon runs `npm run css` (typically `tailwindcss --watch`). The `css`
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
