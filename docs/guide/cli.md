# CLI reference

`hamr` scaffolds projects, runs the dev server, and bundles the generators, linters and helpers a HAMR project uses. Every command and flag is listed here.

```text
hamr
├── new [name]              Scaffold a new project
├── dev                     Start dev server with file watching + live reload
├── setup                   Interactive AI-agent setup (MCP bridge, permissions, skills)
├── compose [args...]       docker compose with hamr dev's merged config
├── env                     Print .env rewrites for hamr dev's port walks
├── mcp                     MCP stdio bridge for AI agents (spawned by the agent)
│   └── install             Register the bridge with claude / codex / opencode
├── add
│   ├── service [name]      Add a new Go binary (worker, api, html, or empty)
│   └── skill [target]      Install AI agent skills (hamr, qa-loop, pr-publish)
├── ai
│   ├── capture <url>       Capture a browser screenshot of a page
│   └── upgrade             Show scaffold changes between versions via git diff
├── gen
│   ├── static              Fingerprint static assets into dist/
│   └── locale              Generate type-safe Go accessors from locale JSON
├── mock-serve              Serve the dev mocks (mail, sms, stripe) headlessly
├── sync                    Sync local directory to S3-compatible bucket
├── vendor [dep[@version]]  Download/checksum frontend JS deps
├── lint
│   └── templ               Lint .templ files
├── rename-module <new-path>  Rename Go module and rewrite imports
├── completion
│   ├── bash                Generate bash completion script
│   ├── zsh                 Generate zsh completion script
│   ├── fish                Generate fish completion script
│   └── install             Install completion scripts for your shell
└── version                 Print hamr version
```

## hamr new

Scaffold a new HAMR project with all required files.

```text
hamr new [name] [flags]
```

An interactive wizard asks for every option not passed as a flag (name and location included). Pass them all to skip the wizard entirely, e.g. in scripts.

| Flag | Default | Description |
|------|---------|-------------|
| `--module` | prompted | Go module path (e.g. `github.com/user/project`) |
| `--css` | `plain` | CSS approach: `plain` or `tailwind` |
| `--storage` | `local` | Storage backend: `local` or `s3` |
| `--websocket` | `false` | Include WebSocket support |
| `--e2e` | `false` | Include E2E testing scaffolding |
| `--database` | `postgres` | Database type: `postgres` or `sqlite` |
| `--db-connector` | `sqlx` | DB connector: `sqlx` or `gorm` |
| `--location` | `subfolder` | Project location: `subfolder` or `current` |
| `--static-s3` | `false` | Sync static assets to a dedicated S3 bucket (only with `--storage s3`) |
| `--stripe` | `false` | Wire stripe-go, the webhook handler, and the local Stripe mock |
| `--email-mock` | `false` | Wire `pkg/emailmock` and the dev inbox at `/__hamr/mail` |
| `--locale` | `false` | Include i18n support (`locales/`, `[locale]`, `hamr gen locale`) |
| `--migrate-startup` | `false` | Run migrations when the server starts instead of via `cmd/migrate` / `make migrate` |
| `--feature-flags` | `false` | Include feature flags (`pkg/flags`): table, store, client wiring, dev widget action, and an admin page when auth is on |
| `--alpine` | `false` | Include Alpine.js for local UI state |
| `--skip-version-check` | `false` | Skip the "is a newer hamr release available" check |

```sh
hamr new myapp                                          # interactive wizard
hamr new myapp --module github.com/user/myapp           # set module path
hamr new myapp --css tailwind --storage s3 --websocket  # wizard still asks the rest
hamr new .                                              # scaffold into current directory
```

Before scaffolding, `hamr new` queries GitHub for the latest release and refuses to run if this binary is out of date — the generated project would otherwise be missing features or templates that the latest hamr ships. Pass `--skip-version-check` to bypass (useful in CI or when offline). Network failures only warn and fall through so a flaky connection never blocks you. Dev builds of the CLI skip the check entirely.

**Guide:** [Storage](pkg/storage.md) covers the `--storage` and `--static-s3` flags in detail.

## hamr dev

Start the development server with file watching, build orchestration, process management, and live reload.

```text
hamr dev [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to config file |
| `--no-proxy` | `false` | Skip the reverse proxy, just run watchers |
| `--verbose`, `-v` | `false` | Enable verbose (debug) logging |
| `--skip-version-check` | `false` | Skip the "scaffold newer than CLI" guard |
| `--headless` | `false` | No TUI: plain log lines on stdout. Automatic when stdout is not a terminal (see [dev guide](pkg/dev.md#headless-mode)). |

```sh
hamr dev                    # reads hamr.toml from current directory
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers
hamr dev --headless > dev.log &   # background, e.g. from an AI agent or CI
hamr dev --verbose          # detailed watcher/rebuild logs
```

If `[hamr].version` in `hamr.toml` is newer than the running CLI, `hamr dev` refuses to start — running an old CLI against a scaffold that depends on newer features leads to silent breakage. Upgrade the CLI or pass `--skip-version-check` to bypass. When the CLI is *newer* than the scaffold, `hamr dev` only warns and continues (the status bar shows the mismatch). Dev builds skip the check entirely.

Reads `[proxy]` and `[dev]` from `hamr.toml` (plus `.pref.hamr.toml`). Manages Docker Compose deps, file watchers, build commands, long-running processes, and a reverse proxy with SSE-based live reload. The proxy runs when `[proxy]` or any of `[dev].proxy_listen` / `proxy_target` / `inject_reload` is set — see [`[proxy]`](hamr-toml.md#proxy--reverse-proxy--live-reload).

By default, `hamr dev` also mirrors its recent log stream to `.hamr/dev_logs.txt` (rolling window, 200 lines, escape sequences stripped) for LLM consumption. Configure via `log_file` and `log_file_max_lines` in `[dev]`.

**Guide:** [Dev Server](pkg/dev.md) covers configuration, watch rules, daemons, Docker Compose, and examples.

On the local listener, hamr's own `/__hamr/*` routes (widget, inboxes, Stripe mock, dev actions) only answer when the Host is `localhost`, `*.localhost` or an IP address; any other hostname gets 403. This blocks DNS-rebinding pages from reading your inboxes or running dev actions. Open the app via `localhost` or an IP, not a custom `/etc/hosts` name.

### TUI

The full hotkey table, status ticker, and log tabs are in [Dev → Terminal Hotkeys](pkg/dev.md#terminal-hotkeys). In short:

- **Build and run:** `r` rebuild all, `R` restart the whole dev server in place (compose, ports, `.env`, builds; also retries while parked on a config error), `o` open the proxy URL, `c` clear the tab.
- **Commands:** `:` fuzzy palette of hamr commands plus every Makefile target as `make:<target>`; `m` opens it pre-filtered to `make:`. Target output streams into the hamr tab prefixed `[make:<target>]`.
- **Toggles:** `M` MCP gateway ([`[dev.mcp]`](hamr-toml.md#devmcp--mcp-gateway-for-ai-agents)), `T` public tunnel ([Public Tunnel](pkg/dev.md#public-tunnel)), `S` Stripe mock ⇄ `stripe listen` ([Stripe Listen Mode](pkg/dev.md#stripe-listen-mode)).
- **Logs:** `Tab` / `Shift+Tab` cycle tabs (🔨 hamr, 🐳 one per `[[dev.docker_compose]]` entry, mcp); the status bar shows `[k/n]` when there's more than one. `/` searches the active tab live; `↩` commits, `n`/`N` step, `f` filters to matching lines. Search is kept per tab, and new lines are matched as they arrive.
- **Selection:** click a line, drag or `Shift+Click` for a range, `Ctrl+Click` to toggle one line, `y` to copy, `Esc` to clear. Selection pauses auto-scroll and is dropped on tab change.
- `?` help, `q` / `Ctrl+C` quit (quit also works while parked on a `hamr.toml` parse error).

Two terminal-dependent details:

- `Shift+Click` and `Ctrl+Click` need the terminal to forward the modifier in its mouse encoding. Alacritty, WezTerm, Kitty, Ghostty and iTerm2 do; some others (older gnome-terminal, certain PuTTY builds) keep shift for native text selection, so those clicks act as plain clicks.
- `y` uses the OS clipboard helper (`xclip` / `wl-copy` on Linux, `pbcopy` on macOS). If it's missing, a `[hamr:tui] clipboard: ...` line appears on the hamr tab.

## hamr mock-serve

```sh
hamr mock-serve
```

Runs the dev mocks (mail, sms, stripe) standalone — no proxy, TUI, build, or watch — for running in a dedicated container in a dev environment. Unlike the mocks embedded in `hamr dev` (which live on the proxy mux and read `hamr.toml`), this command is configured entirely through environment variables and depends on no config file.

Each release publishes it as an image, `ghcr.io/fyrmforge/hamr:<version>` (and `:latest`, linux/amd64 + arm64), whose default command is `hamr mock-serve`. See [Deploying Mocks](pkg/mock-serve.md).

Two listeners: app-facing `HAMR_MOCK_PORT` (Stripe `/v1/*`, mail/SMS ingest) and an optional dashboards port `HAMR_MOCK_UI_PORT` (unset → shares the app-facing port). Both bind all interfaces by default. **The mock surfaces are open by default**: gate dashboards with `HAMR_MOCK_UI_PASSWORD` ([Password gate](#password-gate)), restrict the app-facing port with `HAMR_MOCK_API_ALLOW`, and see [Deploying Mocks → Security](pkg/mock-serve.md#security) for publishing ports, allowlist pitfalls, and TLS.

| Env | Purpose | Default |
| --- | --- | --- |
| `HAMR_MOCKS` | comma-separated mocks to start (required), e.g. `mail,sms,stripe` | — |
| `HAMR_MOCK_PORT` | app-facing port (stripe `/v1`, mail/sms ingest) | `4500` |
| `HAMR_MOCK_UI_PORT` | dashboards port; unset → UI on `HAMR_MOCK_PORT` | unset |
| `HAMR_MOCK_BIND` | bind host for both listeners; empty → all interfaces | empty |
| `HAMR_MOCK_UI_PASSWORD` | gate password for `/__hamr/*` (cookie via `/__hamr/gate`, or `X-Hamr-Gate` header); empty → open. Required in front-door mode | empty |
| `HAMR_MOCK_API_ALLOW` | IPs/CIDRs allowed on the app-facing port, e.g. the app's address `172.30.0.10`; empty → open | empty |
| `HAMR_MAIL_MAX_MESSAGES` | inbox cap | `500` |
| `HAMR_MAIL_MAX_MESSAGE_BYTES` | per-message byte cap | `10MiB` |
| `HAMR_MAIL_PERSIST_PATH` | mbox path; empty → in-memory only | empty |
| `HAMR_SMS_MAX_MESSAGES` | SMS inbox cap | `500` |
| `HAMR_SMS_PERSIST_PATH` | JSONL path; empty → in-memory only | empty |
| `HAMR_MOCK_UPSTREAM` | front-door mode: the app's private `http://host:port`. Requires `HAMR_MOCK_UI_PORT` and `HAMR_MOCK_UI_PASSWORD` | unset |
| `HAMR_BASE_URL` | public origin, e.g. `https://staging.example.com`; `https://` makes the gate cookie `Secure`. Front door: default for `HAMR_STRIPE_BASE_URL` | — |
| `HAMR_GATE_BYPASS` | comma-separated paths that skip the gate, matched on whole path segments | empty |
| `HAMR_ACTIONS_TOKEN` | front door: same token as the app's `HAMR_ACTIONS_TOKEN`; unset → no app actions in the widget | empty |
| `HAMR_STRIPE_BASE_URL` | browser-reachable origin of the mock UI (required for stripe; front door: defaults to `HAMR_BASE_URL`) | — |
| `HAMR_STRIPE_WEBHOOK_URL` | app's webhook handler (required for stripe) | — |
| `HAMR_STRIPE_THIN_WEBHOOK_URL` | app's v2 thin-event handler; empty → thin events are not sent | empty |
| `HAMR_STRIPE_WEBHOOK_SECRET` | matches the app's `STRIPE_WEBHOOK_SECRET` (required for stripe) | — |
| `HAMR_STRIPE_PERSIST_PATH` | state JSON path; empty → in-memory only | empty |

Stripe has no sensible auto-default for its base URL in a container (`localhost:<port>` isn't reachable from the host's published port or sibling containers), so `HAMR_STRIPE_BASE_URL` is required whenever stripe is selected, alongside the webhook url + secret. Point your app at the mock by setting its `HAMR_DEV_URL` / Stripe base to `HAMR_MOCK_PORT`'s URL.

### Password gate

When `HAMR_MOCK_UI_PASSWORD` is set:

- Browsers get a password page at `/__hamr/gate`. A correct password sets an `HttpOnly`, `SameSite=Lax` cookie `__hamr_gate`, valid 7 days (enforced by the server; changing the password invalidates every cookie). The cookie is `Secure` when `HAMR_BASE_URL` is `https://`.
- Scripts send `X-Hamr-Gate: <password>`.
- The cookie and header are stripped before a request reaches the app; the app's own `Authorization` header passes untouched.
- Unauthenticated HTML GETs redirect to the gate page; everything else gets `401` JSON.
- `HAMR_GATE_BYPASS` lists paths that skip the gate (for example provider webhooks that hit the public domain). Matching is on whole path segments: `/payments` covers `/payments/x` but not `/payments-admin`.

In plain mode the gate covers `/__hamr/*` only. In front-door mode it covers the whole site.

### Front-door mode (staging)

Set `HAMR_MOCK_UPSTREAM` and `hamr mock-serve` sits on the public domain in front of your app, so testers on a deployed dev/staging environment get the captured mail/SMS, the Stripe mock, the hamr widget and [dev actions](dev-actions.md) behind one password.

- `HAMR_MOCK_UI_PORT` is the **public** port. `/__hamr/*` (dashboards, gate, widget) is served by hamr; everything else is proxied to the upstream with the widget injected into HTML. Response-header timeout to the upstream is 60s.
- `HAMR_MOCK_PORT` stays the **private** port (Stripe `/v1` `/v2` API, mail/SMS ingest). It is never gated and must not be exposed on the public domain.
- `HAMR_MOCK_UPSTREAM` must be `http://` and requires both `HAMR_MOCK_UI_PORT` and `HAMR_MOCK_UI_PASSWORD`. Missing either is a startup error: the front door fails closed rather than expose an ungated site.
- `/_devactions` always returns 404 on the public port. `/__hamr/app/actions` and `/__hamr/app/whoami` are served there behind the gate when `HAMR_ACTIONS_TOKEN` is set, so anyone with the gate password can run the app's actions. hamr calls the app's `/_devactions` itself, server-side, with the token.
- Client IP: hamr keeps the edge's `X-Forwarded-For` and appends its own peer (the edge), and passes `X-Real-IP` through untouched. On Railway, read the visitor from `X-Real-IP` (the edge overwrites it); the first `X-Forwarded-For` entry is client-set and can be faked.

Railway / compose-style example:

```yaml
services:
  mocks:                          # the public service; attach the domain here
    image: ghcr.io/fyrmforge/hamr:latest
    environment:
      HAMR_MOCKS: mail,sms,stripe
      HAMR_MOCK_UPSTREAM: http://app:8080
      HAMR_MOCK_UI_PORT: "8000"   # public
      HAMR_MOCK_PORT: "4500"      # private, app-only
      HAMR_MOCK_UI_PASSWORD: ${TESTER_PASSWORD}
      HAMR_BASE_URL: https://staging.example.com
      HAMR_GATE_BYPASS: /webhooks
      HAMR_ACTIONS_TOKEN: ${ACTIONS_TOKEN}
      HAMR_STRIPE_WEBHOOK_URL: http://app:8080/webhooks/stripe
      HAMR_STRIPE_WEBHOOK_SECRET: whsec_mock
  app:                            # no public domain
    environment:
      HAMR_ACTIONS_TOKEN: ${ACTIONS_TOKEN}
      HAMR_ACTIONS_ENV: staging
      HAMR_DEV_URL: http://mocks:4500
```

## hamr compose

```text
hamr compose [--name <entry>] [docker compose args...]
```

Passthrough to `docker compose` with this project's compose file **and** the generated port-walk override when one exists.

When a host port is busy, `hamr dev` walks it and records the result in `.hamr/compose.<name>.override.yaml`. A plain `docker compose -f docker/docker-compose.yaml ...` merges only the base file, decides the running stack has drifted, and recreates it on the original ports — which fails outright when those ports are busy, which is why they were walked:

```text
Error response from daemon: Bind for 0.0.0.0:9000 failed: port is already allocated
```

`hamr compose` merges what hamr merges, so external callers stop fighting the dev server:

```sh
hamr compose up -d
hamr compose down -v
hamr compose exec -T postgres psql -U postgres
hamr compose --name deps logs -f
```

`--name` picks the `[[dev.docker_compose]]` entry and is required when there is more than one. Everything after the first argument goes to docker untouched; stdin, stdout, stderr, and the exit code pass straight through. Runs from anywhere inside the project.

This is the compose-side counterpart to [`hamr env`](#hamr-env), which solves the same problem for walked ports in shell scripts.

The scaffold's `make docker-up` / `docker-down` / `docker-delete` targets use it. In an environment without the `hamr` binary (a CI image that only needs the stack up, with no walking in play), plain `docker compose -f docker/docker-compose.yaml ...` is still fine.

## hamr setup

```text
hamr setup [--dry-run]
```

Interactive picker for this project's AI-agent integration. One screen per decision:

1. **MCP bridge** — which agents (`claude`, `codex`, `opencode`) get the hamr bridge registered. Already-installed agents start ticked. Writes the same per-agent config as [`hamr mcp install`](#hamr-mcp-install) — merged, never clobbered.
2. **Gateway enabled** — sets `[dev.mcp].enabled` in `hamr.toml`.
3. **Tool permissions** — `deny` / `read` / `write` per area (`dev`, `logs`, `docker`, `mail`, `sms`, `build`, `stripe`), seeded from the current `[dev.mcp.access]`. `write` implies `read`; a denied area exposes none of its tools.
4. **Agent skills** — which agents get skills (`claude` → `.claude/skills/`, `codex`/`opencode` → shared `.agents/skills/`), then which skills (`hamr`, `qa-loop`, `pr-publish`) — the same content as [`hamr add skill`](#hamr-add-skill). Selected skills overwrite an existing install.

It also upserts a `## hamr MCP` section into `AGENTS.md` and `CLAUDE.md`, listing only the tools the granted areas actually expose — agents that never read the config otherwise default to doing the same work by hand (tailing logs, running `make build`, asking what an email said). Disabling the gateway removes the section again, so the instructions never point at tools that aren't there. Missing instruction files are skipped, not created.

Only `[dev.mcp]` and `[dev.mcp.access]` are rewritten in `hamr.toml`, and only the `## hamr MCP` section in the instruction files — comments, ordering, and every other table or section are left alone. `--dry-run` prints what would change and writes nothing.

Run it from anywhere inside the project; the nearest `hamr.toml` wins. Needs a TTY.

## hamr mcp

```text
hamr mcp [--project <path>]
```

Runs the Model Context Protocol bridge over stdio so an AI agent (Claude Code, Codex, opencode) can drive a running `hamr dev`. The agent spawns this; it is not run by hand. The bridge:

- resolves the project (the `--project` path, else the nearest `hamr.toml` from the working directory),
- advertises exactly the tools the project's `[dev.mcp.access]` map exposes,
- forwards each tool call to the dev server's `/__hamr/mcp/*` gateway, authenticated with the per-run token in `.hamr/dev.json`.

`hamr dev` must be running with `[dev.mcp].enabled` (or toggled on with `M`); otherwise tool calls return a clear "dev not running / gateway off" error. See [`[dev.mcp]`](hamr-toml.md) for the config, tools, and security model.

### hamr mcp install

```text
hamr mcp install [--client claude|codex|opencode] [--dry-run]
```

Registers the bridge with an agent by writing its config (merging, never clobbering; idempotent). With no `--client`, auto-detects installed agents and configures each. Claude (`.mcp.json`) and opencode (`opencode.json`) are project-scoped; Codex (`~/.codex/config.toml`) is global, so its entry is pinned to this project with `--project`. `--dry-run` previews without writing. Requires `hamr` on your `PATH`.

## hamr add service

Add a new Go binary to an existing HAMR project. Creates `cmd/<name>/` (main.go + Dockerfile) and `internal/<name>/`, appends a `[[dev.watch]]` rule to `hamr.toml` so `hamr dev` builds and runs the binary alongside the site, and — for the HTTP types — appends a `<NAME>_PORT` entry to `.env` and `.env.example`.

```text
hamr add service [name] [flags]
```

Must be run from the root of a HAMR project (a directory containing `hamr.toml`). Any option not provided as a flag is asked interactively, mirroring `hamr new`.

Service types:

| Type | What you get |
|------|--------------|
| `worker` | Background worker — signal handling, graceful shutdown, ticker loop stub, optional DB |
| `api` | JSON API server — `pkg/server` on its own port, `/api/health` route, optional DB and session auth |
| `html` | HTML web server — `pkg/server` + templ on its own port, reuses the project's `internal/web/components` layout, optional DB/auth/locale |
| `empty` | Bare binary — env/logging bootstrap and a `Run()` stub |

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | prompted | Service type: `empty`, `worker`, `api`, or `html` |
| `--db` | prompted | Wire the project's repo store (uses the database/connector from `hamr.toml [options]`) |
| `--auth` | prompted | Wire session auth middleware against the shared store (`api`/`html`; requires `--db` and a project scaffolded with session auth). The site keeps owning login — the service only validates sessions. |
| `--locale` | prompted | Load the project's locale bundle + middleware (`html`; requires a project scaffolded with locale) |
| `--port` | `8081` | Listen port for `api`/`html` services, exposed as `<NAME>_PORT` |
| `--skip-version-check` | `false` | Bypass the `--auth` guard that refuses when the CLI is newer than the project's `[hamr] version` |

```sh
hamr add service                                   # fully interactive
hamr add service mailer --type worker --db
hamr add service billing --type api --db --auth --port 8081
hamr add service admin --type html --db --auth=false --port 8082
```

Notes:

- HTTP services run on their own port and are **not** behind the `hamr dev` proxy at `:3000` — hit them directly (`http://localhost:<port>`). Live-reload injection and the browser console capture apply to the proxied site only; the appended watch rule still rebuilds and restarts the service on change.
- The service name becomes the binary (`bin/<name>`), the watch-rule name, and the env prefix (`billing-svc` → `BILLING_SVC_PORT`). Names already used by a directory or watch rule are rejected.
- Migrations stay owned by the site / `cmd/migrate`; services connect to the schema as-is.
- `--auth` (released builds only) refuses when the CLI is newer than the project's `[hamr] version`: the generated `SubjectLoader` calls `deps.Store.Users().GetByID`, and `internal/repo` is project-owned code that does not upgrade with the CLI. Migrate it by hand (`hamr ai upgrade` shows the diff), then `hamr ai upgrade --applied`, or pass `--skip-version-check`.

## hamr add skill

Install AI agent skills for working on a HAMR project. Three are available:

- **`hamr`** — the framework skill: the CLI, the `pkg/*` packages, and the project's Go + templ + HTMX conventions (plus Alpine.js guidance when the project opted in). Rendered per project: it reads `hamr.toml` to tailor its content; `--global` installs skip this and use default-off assumptions.
- **`qa-loop`** — an iterative QA test-and-fix loop that drives the app with Playwright MCP across device profiles, checks functional + UI/UX watch-lists, scores each round, and fixes findings. Uses hamr MCP for everything server-side.
- **`pr-publish`** — creates or refreshes a GitHub PR with a structured body, gist-hosted screenshots, and a stepped flow GIF captured from the live dev server.

With no arguments an interactive picker asks which agents and which skills to install (detected agents and already-installed skills start ticked — or an explicit `--skills` seeds the selection — and selected skills overwrite existing installs). With a target argument it runs non-interactively.

```text
hamr add skill [target] [flags]
```

| Flag        | Default | Description                                                           |
|-------------|---------|-----------------------------------------------------------------------|
| `--global`  | `false` | Install under the home directory instead of the project               |
| `--force`   | `false` | Overwrite existing skill directories (non-interactive mode)           |
| `--skills`  | all     | Which skills to install, comma-separated: `hamr`, `qa-loop`, `pr-publish` |

```sh
hamr add skill                          # interactive picker
hamr add skill claude                   # all skills → ./.claude/skills/{hamr,hamr-qa-loop,hamr-pr-publish}/
hamr add skill codex                    # all skills → ./.agents/skills/…
hamr add skill claude --skills hamr     # just the framework skill
hamr add skill claude --global --force  # user-global, replacing existing installs
```

Supported targets: `claude`, `codex`, `opencode`. The SKILL.md format is a cross-tool standard, so all targets get identical content — only the destination differs. `claude` installs to `.claude/skills/` (global: `~/.claude/skills/`); `codex` and `opencode` both read the shared `.agents/skills/` (global: `~/.agents/skills/`), so selecting both writes once. opencode also reads `.claude/skills/` natively, so a claude install already covers opencode users.

Project-local installs must be run from the root of a HAMR project (a directory containing `hamr.toml`) and are typically committed so the whole team benefits. Global installs work from any directory and persist per-user.

## hamr ai capture

Capture a browser screenshot of a page for debugging, visual review, or LLM workflows.

```text
hamr ai capture <url> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--out` | — | Output PNG path |
| `--dir` | `<[ai].dir>/captures` (`.hamr/ai/captures`) | Root directory for per-capture folders |
| `--browser` | auto-detect | Browser binary path |
| `--text` | `false` | Also save visible page text to a `.txt` sidecar |
| `--html` | `false` | Also save page HTML to a `.html` sidecar |
| `--json` | `false` | Print capture metadata as JSON |
| `--selector` | — | Capture only the first element matching this CSS selector |
| `--full-page` | `false` | Capture the full scrollable page instead of only the viewport |
| `--headless` | `true` | Run Chromium headlessly |
| `--no-sandbox` | `true` | Launch Chromium with `--no-sandbox` |
| `--width` | `1440` | Viewport width in pixels |
| `--height` | `1024` | Viewport height in pixels |
| `--scale` | `1` | Device scale factor |
| `--scroll-to` | — | Scroll to `top`, `middle`, or `bottom` before capture |
| `--scroll-x` | `0` | Horizontal scroll offset in pixels |
| `--scroll-y` | `0` | Vertical scroll offset in pixels |
| `--scroll-selector` | — | CSS selector for a scroll container; defaults to the window |
| `--tiles` | `false` | Capture viewport-sized tiles from top to bottom |
| `--tile-overlap` | `120` with `--tiles` | Pixels of overlap between tiles; requires `--tiles` |
| `--timeout` | `15s` | Timeout for browser operations |
| `--wait` | `1s` | Extra delay after page load before capture |

```sh
hamr ai capture http://localhost:3000
hamr ai capture localhost:3000/login --text --html --json
hamr ai capture https://example.com --selector '#app' --dir /tmp/captures
hamr ai capture https://example.com/docs --full-page --wait 2s
hamr ai capture https://example.com/docs --tiles --tile-overlap 200
hamr ai capture https://example.com/pricing --scroll-to middle --width 1280 --height 720
hamr ai capture https://example.com/dashboard --scroll-selector '.results-pane' --scroll-to bottom
```

By default the command creates a per-capture folder under `.hamr/ai/captures/` (`[ai].dir` in `hamr.toml` moves the base) and writes `screenshot.png`, optional `screenshot.txt` / `screenshot.html`, plus `meta.json`. `--out` lets you force a specific PNG path instead. `--tiles` writes `tile_001.png`, `tile_002.png`, … instead, for pages too tall to read as one image; it can't be combined with `--full-page`, `--selector`, or the scroll flags.

## hamr ai upgrade

Show scaffold changes between the project's baseline version and the current HAMR version by diffing the actual HAMR repository between version tags.

The diff is scoped to the surface a downstream project consumes or mirrors — the
`pkg/` libraries it imports, the scaffold templates its files were generated from
(`internal/cli/generator/templates/`), and the `docs/` + `llmsdocs/` the project
carries and can bring up to date. Only hamr's own internal tooling (the CLI
commands, dev server, generator logic, the `hamr` binary) and tests are
excluded — none of it ever lands in a project. This keeps the report focused on
what an upgrade must adapt to rather than the whole framework diff.

```text
hamr ai upgrade [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as JSON |
| `--from` | — | Override the base version to diff from |
| `--applied` | `false` | Record the project baseline as the current HAMR version (or the `--from` version) |
| `--dir` | `<[ai].dir>/upgrades` (`.hamr/ai/upgrades`) | Directory to save the upgrade report |

```sh
hamr ai upgrade                    # diff between project version and current HAMR
hamr ai upgrade --json             # structured JSON output
hamr ai upgrade --from 0.1.0      # override the base version
hamr ai upgrade --applied          # bump [hamr] version in hamr.toml
hamr ai upgrade --applied --from 0.44.0  # record a specific version
```

The command clones the HAMR repository (bare, partial clone for speed) and runs `git diff` between the two version tags. The output is a unified diff and stat summary of the scoped paths above.

`--applied` refuses to move the baseline backwards (an older CLI than the recorded `[hamr].version`); pass `--from` to set a version explicitly.

Reports are saved to `.hamr/ai/upgrades/` (under `[ai].dir`) as JSON files. An LLM agent can consume the structured output to present changes conversationally and guide the developer through what to adopt.

## hamr gen static

Fingerprint static assets by content-hashing filenames.

```text
hamr gen static [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--clean` | `false` | Remove the dist dir and reset the generated manifest |

```sh
hamr gen static          # fingerprint frontend/static/ → frontend/dist/
hamr gen static --clean  # remove frontend/dist/ and reset manifest
```

Reads source files from `[static].dir` (`frontend/static/` in current scaffolds), creates fingerprinted copies (e.g. `output.a1b2c3d4e5f6.css`) in `[static].dist`, and generates a Go source file with the manifest baked in at compile time. The `make build` target runs this automatically before `go build`.

**Configuration** via `hamr.toml`:

```toml
[static]
dir = "frontend/static"
dist = "frontend/dist"
manifest = "internal/web/components/staticmanifest.go"
package = "components"
```

**Guide:** [Static assets](static-assets.md) covers fingerprinting, cache headers, and deployment.

## hamr sync

Sync a local directory to an S3-compatible bucket.

```text
hamr sync [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `[static].dir`, else `static` | Local directory to sync |
| `--watch` | `false` | Watch for changes after initial sync |
| `--endpoint` | `S3_ENDPOINT`, else `http://localhost:9000` | S3 endpoint URL |
| `--bucket` | `S3_BUCKET` | S3 bucket name |
| `--region` | `S3_REGION`, else `us-east-1` | S3 region |
| `--access-key` | `S3_ACCESS_KEY` | S3 access key |
| `--secret-key` | `S3_SECRET_KEY` | S3 secret key |
| `--path-style` | `true` | Use path-style addressing (required for RustFS) |

```sh
hamr sync                              # one-shot sync of [static].dir to S3
hamr sync --watch                      # watch for changes and sync continuously
hamr sync --dir dist --bucket my-cdn   # sync a different directory to a specific bucket
```

Each S3 setting resolves flag → shell env var → `.env` in the current directory → built-in default. Values read from `.env` get `hamr dev`'s port-walk rewrites from `.hamr/walks.json`, so a walked `S3_ENDPOINT` just works with no manual override. `[static].dir` is read from `hamr.toml` and `.pref.hamr.toml` in the current directory.

**Guide:** [Sync](pkg/sync.md) covers the Go library API. [Storage](pkg/storage.md) covers S3 setup.

## hamr env

Print env-var rewrites derived from the running dev server's port walks. Reads `.hamr/walks.json` (written by `hamr dev` after walking) and `.env`, applies the same rewrite rules `hamr dev` uses internally, and emits the resulting `KEY=VALUE` pairs to stdout.

```sh
hamr env             # KEY=VALUE form
hamr env --export    # export KEY=VALUE form, single-quoted for safe sourcing
```

| Flag | Default | Description |
|------|---------|-------------|
| `--export` | `false` | Emit `export KEY=VALUE` for shell sourcing |
| `--dir` | `.` | Project root to resolve `.hamr/walks.json` and `.env` from (use this when invoking from a script that may not run with the project root as cwd) |

When no walks are recorded (port_walk disabled, every port was free, or `hamr dev` isn't running), the command exits 0 with no output — sourcing it is a no-op and the consumer falls through to `.env` unchanged.

```make title="Makefile"
SHELL := bash
.SHELLFLAGS := -ec
ENV_LOAD := eval "$$(hamr env --export 2>/dev/null || true)";

migrate:
	$(ENV_LOAD) go run ./cmd/migrate
```

The scaffold's `Makefile` ships this prefix on its DB and e2e targets already (`migrate*`, `seed`, `db-sh`, `generate`, `e2e-local`, `e2e-run-local`).

Which `.env` values get rewritten (and which don't) is documented in [`[dev].port_walk`](hamr-toml.md#port-walks--collision-tolerant-port-binding).

## hamr lint templ

Lint `.templ` files for common issues.

```text
hamr lint templ [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rule` | all | Run only these rules (comma-separated IDs) |
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--severity` | all | Minimum severity to report: `warning` or `error` |

```sh
hamr lint templ                          # lint current directory (recursive)
hamr lint templ --rule inline-if,img-alt # run only specific rules
hamr lint templ --severity error         # only report errors
hamr lint templ --config my-hamr.toml    # use a custom config file
```

**Exit codes:** `0` if no error-severity diagnostics, `1` if any errors found.

**Available rules** (default severity — what the scaffold writes):

| ID | Severity | Description |
|----|----------|-------------|
| `inline-if` | error | Inline if with HTML body (silently dropped by templ) |
| `inline-for` | error | Inline for with HTML body (silently dropped) |
| `inline-switch` | error | Inline switch with HTML body (silently dropped) |
| `no-native-form-actions` | error | `action=`, `method=`, `formaction=` (use `hx-*` instead) |
| `htmx-conflict` | error | Same element has both `hx-*` and a native form attribute |
| `img-alt` | warning | `<img>` missing `alt` attribute |
| `no-href` | warning | `<a>` missing `href` attribute |
| `inline-style` | warning | Inline `style` attributes |
| `empty-class` | warning | Empty `class=""` attributes |
| `js-href` | warning | `href="javascript:..."` links |

Configure in `hamr.toml` under [`[lint.templ]`](hamr-toml.md#linttempl--templ-linter-rules) — each rule mapped to `"warning"`, `"error"`, or `"off"`. Rules not listed are disabled, so with no `[lint.templ]` table nothing is reported. Unknown rule IDs and invalid severities fail loudly.

**Inline suppression.** A `templint:ignore` directive in a `//` comment silences diagnostics on the next line, or on its own line when it trails source content:

```templ
// templint:ignore no-native-form-actions -- manifest flow needs a browser POST
<form method="post" action={ templ.SafeURL(action) }>

<img src="logo.png">           // templint:ignore img-alt
// templint:ignore img-alt, empty-class    (comma-separated)
// templint:ignore                          (all rules)
```

Everything after ` -- ` is a human reason. Rules anchor diagnostics to the line a tag *opens* on, so for a multi-line tag the directive goes above the `<form`, not above the offending attribute. Two extra diagnostics keep directives honest: `unknown-rule` (error) for a mistyped rule ID, and `unused-suppression` (warning) for a directive that suppresses nothing. Neither is configurable; a directive naming a rule that is switched `"off"` is silently ignored.

**Guide:** [Templint](pkg/templint.md) covers rules, configuration, and library usage.

## hamr gen locale

Generate type-safe Go accessor methods from locale JSON files.

```text
hamr gen locale [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--dir` | from config | Locale directory (overrides config) |
| `--out` | from config | Output file path (overrides config) |

```sh
hamr gen locale                            # use hamr.toml settings
hamr gen locale --dir locales --out internal/locale/locale.go
```

Reads the default locale JSON, flattens all keys, and generates a Go file with
a `T` wrapper struct containing a typed method per translation key. Non-default
locales are validated — interpolation mismatches are errors, missing keys are
warnings.

Keys starting with a digit (e.g. `2fa.title`) produce method names prefixed
with `X` (e.g. `X2faTitle`).

**Configuration** via `hamr.toml`:

```toml
[locale]
default = "en"
dir     = "locales"
output  = "internal/locale/locale.go"
package = "locale"
```

The scaffolded `Makefile` runs `hamr gen locale` as part of `make build` and
`make test` when the project includes locale support.

**Guide:** [I18n](pkg/i18n.md) covers the runtime library.

## hamr vendor

Download and checksum frontend JavaScript dependencies.

```text
hamr vendor [dep[@version]] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--update` | `false` | Re-download all dependencies at latest versions |
| `--verify` | `false` | Verify checksums of vendored files |
| `--url` | — | Custom URL to download |
| `--out` | — | Output path for custom URL (relative to project root) |

```sh
hamr vendor                          # vendor all deps at default/locked versions
hamr vendor htmx                     # vendor only htmx
hamr vendor alpine@3.14.9            # vendor alpine at pinned version
hamr vendor --update                 # re-vendor all at latest
hamr vendor --verify                 # check checksums
hamr vendor --url <url> --out <path> # custom dependency
```

Downloads files to `frontend/static/js/` and records checksums in `hamr.vendor.json`. Built-in deps: `htmx`, `alpine`, `idiomorph`.

## hamr rename-module

Rename the Go module path and rewrite all import paths.

```text
hamr rename-module <new-module-path> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `.` | Directory containing go.mod |
| `--dry-run` | `false` | Show what would change without writing files |

```sh
hamr rename-module github.com/neworg/myproject
hamr rename-module github.com/neworg/myproject --dry-run
hamr rename-module github.com/neworg/tools --dir ./tools
```

Reads the current module path from `go.mod`, then replaces it in every `.go` and `.templ` file and the `go.mod` module directive. The `.git`, `vendor`, `node_modules`, and `testdata` directories are skipped — vendored and third-party code must not be rewritten, and `testdata` is excluded from Go builds (and often holds intentionally malformed fixtures).

## hamr version

Print the hamr version and commit hash.

```sh
hamr version
```

Output: `hamr <version> (<commit>)`

## hamr completion

Generate or install shell completion scripts for bash, zsh, or fish.

### Generate to stdout

```sh
hamr completion bash
hamr completion zsh
hamr completion fish
```

### Install

```sh
hamr completion install              # per-user install (auto-detects shell from $SHELL)
hamr completion install --shell zsh  # override shell detection
hamr completion install --system     # system-wide install (requires root)
```

| Flag | Default | Description |
|------|---------|-------------|
| `--shell` | auto-detect | Target shell: `bash`, `zsh`, or `fish` |
| `--system` | `false` | Install system-wide instead of per-user |
| `-y`, `--yes` | `false` | Skip confirmation prompt |

**Per-user paths:**

| Shell | Path |
|-------|------|
| bash  | `~/.local/share/bash-completion/completions/hamr` |
| zsh   | `~/.zsh/completions/_hamr` |
| fish  | `~/.config/fish/completions/hamr.fish` |

**System-wide paths:**

| Shell | Path |
|-------|------|
| bash  | `/usr/share/bash-completion/completions/hamr` |
| zsh   | `/usr/share/zsh/site-functions/_hamr` |
| fish  | `/usr/share/fish/vendor_completions.d/hamr.fish` |

For zsh per-user installs, you may need to add the following to `~/.zshrc`:

```text title="~/.zshrc"
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

## Environment

The CLI never loads `.env` into its own process env; commands that need a value read just that key:

- `hamr sync` — S3 settings when neither flag nor shell env var is set.
- `hamr env` — reads `.env` to emit port-walk rewrites.
- `hamr dev` — resolves `$VAR` in `proxy_listen` / `proxy_target` (shell env first, then `.env` next to `hamr.toml`), applies port-walk rewrites to `.env` values for spawned processes, and reads `STRIPE_KEY` for `stripe listen`.
