# CLI Reference

`hamr` is a Go full-stack framework CLI for project scaffolding, development, and tooling.

```
hamr
├── new [name]              Scaffold a new project
├── dev                     Start dev server with file watching + live reload
├── setup                   Interactive AI-agent setup (MCP bridge, permissions, skills)
├── compose [args...]       docker compose with hamr dev's merged config
├── add
│   ├── service [name]      Add a new Go binary (worker, api, html, or empty)
│   └── skill <target>      Install an AI agent skill describing hamr
├── ai
│   ├── capture <url>       Capture a browser screenshot of a page
│   └── upgrade             Show scaffold changes between versions via git diff
├── gen
│   ├── static              Fingerprint static assets into dist/
│   └── locale              Generate type-safe Go accessors from locale JSON
├── mock-serve              Serve the dev mocks (mail, stripe) headlessly
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

---

## hamr new

Scaffold a new HAMR project with all required files.

```bash
hamr new [name] [flags]
```

When called without flags, an interactive wizard asks for each option. When all flags are provided, no prompts are shown.

| Flag | Default | Description |
|------|---------|-------------|
| `--module` | prompted | Go module path (e.g. `github.com/user/project`) |
| `--css` | `plain` | CSS approach: `plain` or `tailwind` |
| `--storage` | `none` | Storage backend: `none`, `local`, or `s3` |
| `--websocket` | `false` | Include WebSocket support |
| `--e2e` | `false` | Include E2E testing scaffolding |
| `--database` | `postgres` | Database type: `postgres` or `sqlite` |
| `--db-connector` | `sqlx` | DB connector: `sqlx` or `gorm` |
| `--location` | `subfolder` | Project location: `subfolder` or `current` |
| `--static-s3` | `false` | Sync static assets to a dedicated S3 bucket |
| `--stripe` | `false` | Include Stripe webhook handler |
| `--alpine` | `false` | Include Alpine.js for local UI state |
| `--skip-version-check` | `false` | Skip the "is a newer hamr release available" check |

```bash
hamr new myapp                                          # interactive wizard
hamr new myapp --module github.com/user/myapp           # set module path
hamr new myapp --css tailwind --storage s3 --websocket  # all features
hamr new .                                              # scaffold into current directory
```

Before scaffolding, `hamr new` queries GitHub for the latest release and refuses to run if this binary is out of date — the generated project would otherwise be missing features or templates that the latest hamr ships. Pass `--skip-version-check` to bypass (useful in CI or when offline). Network failures only warn and fall through so a flaky connection never blocks you. Dev builds of the CLI skip the check entirely.

**Guide:** [Storage](pkg/storage.md) covers the `--storage` and `--static-s3` flags in detail.

---

## hamr dev

Start the development server with file watching, build orchestration, process management, and live reload.

```bash
hamr dev [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to config file |
| `--no-proxy` | `false` | Skip the reverse proxy, just run watchers |
| `--verbose`, `-v` | `false` | Enable verbose (debug) logging |
| `--skip-version-check` | `false` | Skip the "scaffold newer than CLI" guard |

```bash
hamr dev                    # reads hamr.toml from current directory
hamr dev --config my.toml   # custom config path
hamr dev --no-proxy         # skip proxy, just run watchers
hamr dev --verbose          # detailed watcher/rebuild logs
```

If `[hamr].version` in `hamr.toml` is newer than the running CLI, `hamr dev` refuses to start — running an old CLI against a scaffold that depends on newer features leads to silent breakage. Upgrade the CLI or pass `--skip-version-check` to bypass. When the CLI is *newer* than the scaffold, `hamr dev` only warns and continues (the status bar shows the mismatch). Dev builds skip the check entirely.

Reads `[proxy]`, `[dev]` sections from `hamr.toml`. Manages Docker Compose deps, file watchers, build commands, long-running processes, and a reverse proxy with SSE-based live reload.

By default, `hamr dev` also mirrors its recent log stream to `.hamr/dev_logs.txt` (rolling window, 200 lines, escape sequences stripped) for LLM consumption. This includes `[hamr dev]` messages plus stdout/stderr from watched commands and daemons. Configure via `log_file` and `log_file_max_lines` in `[dev]`.

**Guide:** [Dev Server](pkg/dev.md) covers configuration, watch rules, daemons, Docker Compose, and examples.

The TUI shows an MCP indicator when `[dev.mcp]` is configured; press `M` to toggle the gateway on/off for the session (a runtime kill-switch that doesn't rewrite `hamr.toml`).

---

## hamr mock-serve

```
hamr mock-serve
```

Runs the dev mocks (mail, stripe) standalone — no proxy, TUI, build, or watch — for running in a dedicated container in a dev environment. Unlike the mocks embedded in `hamr dev` (which live on the proxy mux and read `hamr.toml`), this command is configured entirely through environment variables and depends on no config file.

Two listeners:

- **app-facing** (`HAMR_MOCK_PORT`): the surface your app talks to — stripe `/v1/*` API and the mail ingest sink.
- **UI** (`HAMR_MOCK_UI_PORT`): the human dashboards (captured email, fake payments). Optional — when unset the UI mounts on the app-facing port too. Splitting it onto its own port lets your deployment expose the two surfaces differently — e.g. publish the app-facing port to the app while keeping the dashboards on a port you only publish to `127.0.0.1`.

Both listeners bind all interfaces by default (`HAMR_MOCK_BIND` empty), which is correct inside a container where sibling containers reach the mock by service name; isolate by controlling which ports you publish. **The mock surfaces are unauthenticated** — the UI serves every captured email (which can contain password-reset tokens and magic-login links) over plain GET, and the Stripe surface will fire a correctly-signed webhook at your app on request. Do not expose them on a reachable interface in a shared environment. When running outside a container on a shared host, set `HAMR_MOCK_BIND=127.0.0.1`.

| Env | Purpose | Default |
| --- | --- | --- |
| `HAMR_MOCKS` | comma-separated mocks to start (required), e.g. `mail,stripe` | — |
| `HAMR_MOCK_PORT` | app-facing port (stripe `/v1`, mail ingest) | `4500` |
| `HAMR_MOCK_UI_PORT` | dashboards port; unset → UI on `HAMR_MOCK_PORT` | unset |
| `HAMR_MOCK_BIND` | bind host for both listeners; empty → all interfaces | empty |
| `HAMR_MAIL_MAX_MESSAGES` | inbox cap | `500` |
| `HAMR_MAIL_MAX_MESSAGE_BYTES` | per-message byte cap | `10MiB` |
| `HAMR_MAIL_PERSIST_PATH` | mbox path; empty → in-memory only | empty |
| `HAMR_STRIPE_BASE_URL` | browser-reachable origin of the mock UI (required for stripe) | — |
| `HAMR_STRIPE_WEBHOOK_URL` | app's webhook handler (required for stripe) | — |
| `HAMR_STRIPE_WEBHOOK_SECRET` | matches the app's `STRIPE_WEBHOOK_SECRET` (required for stripe) | — |
| `HAMR_STRIPE_PERSIST_PATH` | state JSON path; empty → in-memory only | empty |

Stripe has no sensible auto-default for its base URL in a container (`localhost:<port>` isn't reachable from the host's published port or sibling containers), so `HAMR_STRIPE_BASE_URL` is required whenever stripe is selected, alongside the webhook url + secret. Point your app at the mock by setting its `HAMR_DEV_URL` / Stripe base to `HAMR_MOCK_PORT`'s URL.

---

## hamr compose

```
hamr compose [--name <entry>] [docker compose args...]
```

Passthrough to `docker compose` with this project's compose file **and** the generated port-walk override when one exists.

When a host port is busy, `hamr dev` walks it and records the result in `.hamr/compose.<name>.override.yaml`. A plain `docker compose -f docker/docker-compose.yaml ...` merges only the base file, decides the running stack has drifted, and recreates it on the original ports — which fails outright when those ports are busy, which is why they were walked:

```
Error response from daemon: Bind for 0.0.0.0:9000 failed: port is already allocated
```

`hamr compose` merges what hamr merges, so external callers stop fighting the dev server:

```bash
hamr compose up -d
hamr compose down -v
hamr compose exec -T postgres psql -U postgres
hamr compose --name deps logs -f
```

`--name` picks the `[[dev.docker_compose]]` entry and is required when there is more than one. Everything after the first argument goes to docker untouched; stdin, stdout, stderr, and the exit code pass straight through. Runs from anywhere inside the project.

This is the compose-side counterpart to [`hamr env`](#hamr-env), which solves the same problem for walked ports in shell scripts.

The scaffold's `make docker-up` / `docker-down` / `docker-delete` targets use it. In an environment without the `hamr` binary (a CI image that only needs the stack up, with no walking in play), plain `docker compose -f docker/docker-compose.yaml ...` is still fine.

---

## hamr setup

```
hamr setup [--dry-run]
```

Interactive picker for this project's AI-agent integration. One screen per decision:

1. **MCP bridge** — which agents (`claude`, `codex`, `opencode`) get the hamr bridge registered. Already-installed agents start ticked. Writes the same per-agent config as [`hamr mcp install`](#hamr-mcp-install) — merged, never clobbered.
2. **Gateway enabled** — sets `[dev.mcp].enabled` in `hamr.toml`.
3. **Tool permissions** — `deny` / `read` / `write` per area (`dev`, `logs`, `docker`, `mail`, `build`, `stripe`), seeded from the current `[dev.mcp.access]`. `write` implies `read`; a denied area exposes none of its tools.
4. **Agent skills** — installs the hamr framework skill, the same content as [`hamr add skill`](#hamr-add-skill). Overwrites an existing skill directory. `claude` only for now.

It also upserts a `## hamr MCP` section into `AGENTS.md` and `CLAUDE.md`, listing only the tools the granted areas actually expose — agents that never read the config otherwise default to doing the same work by hand (tailing logs, running `make build`, asking what an email said). Disabling the gateway removes the section again, so the instructions never point at tools that aren't there. Missing instruction files are skipped, not created.

Only `[dev.mcp]` and `[dev.mcp.access]` are rewritten in `hamr.toml`, and only the `## hamr MCP` section in the instruction files — comments, ordering, and every other table or section are left alone. `--dry-run` prints what would change and writes nothing.

Run it from anywhere inside the project; the nearest `hamr.toml` wins. Needs a TTY.

---

## hamr mcp

```
hamr mcp [--project <path>]
```

Runs the Model Context Protocol bridge over stdio so an AI agent (Claude Code, Codex, opencode) can drive a running `hamr dev`. The agent spawns this; it is not run by hand. The bridge:

- resolves the project (the `--project` path, else the nearest `hamr.toml` from the working directory),
- advertises exactly the tools the project's `[dev.mcp.access]` map exposes,
- forwards each tool call to the dev server's `/__hamr/mcp/*` gateway, authenticated with the per-run token in `.hamr/dev.json`.

`hamr dev` must be running with `[dev.mcp].enabled` (or toggled on with `M`); otherwise tool calls return a clear "dev not running / gateway off" error. See [`[dev.mcp]`](hamr-toml.md) for the config, tools, and security model.

### hamr mcp install

```
hamr mcp install [--client claude|codex|opencode] [--dry-run]
```

Registers the bridge with an agent by writing its config (merging, never clobbering; idempotent). With no `--client`, auto-detects installed agents and configures each. Claude (`.mcp.json`) and opencode (`opencode.json`) are project-scoped; Codex (`~/.codex/config.toml`) is global, so its entry is pinned to this project with `--project`. `--dry-run` previews without writing. Requires `hamr` on your `PATH`.

---

## hamr add service

Add a new Go binary to an existing HAMR project. Creates `cmd/<name>/` (main.go + Dockerfile) and `internal/<name>/`, appends a `[[dev.watch]]` rule to `hamr.toml` so `hamr dev` builds and runs the binary alongside the site, and — for the HTTP types — appends a `<NAME>_PORT` entry to `.env` and `.env.example`.

```bash
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

```bash
hamr add service                                   # fully interactive
hamr add service mailer --type worker --db
hamr add service billing --type api --db --auth --port 8081
hamr add service admin --type html --db --auth=false --port 8082
```

Notes:

- HTTP services run on their own port and are **not** behind the `hamr dev` proxy at `:3000` — hit them directly (`http://localhost:<port>`). Live-reload injection and the browser console capture apply to the proxied site only; the appended watch rule still rebuilds and restarts the service on change.
- The service name becomes the binary (`bin/<name>`), the watch-rule name, and the env prefix (`billing-svc` → `BILLING_SVC_PORT`). Names already used by a directory or watch rule are rejected.
- Migrations stay owned by the site / `cmd/migrate`; services connect to the schema as-is.

---

## hamr add skill

Install an AI agent skill that teaches the target tool about HAMR — the CLI, the `pkg/*` packages, and the project's Go + templ + HTMX conventions (plus Alpine.js guidance when the project opted in). The skill reads `hamr.toml` from the current project to tailor its content; `--global` installs skip this and use default-off assumptions.

```bash
hamr add skill <target> [flags]
```

| Flag       | Default | Description                                                           |
|------------|---------|-----------------------------------------------------------------------|
| `--global` | `false` | Install to `~/.<target>/skills/hamr/` instead of `./.<target>/skills/hamr/` |
| `--force`  | `false` | Overwrite an existing skill directory                                 |

```bash
hamr add skill claude            # project-local: ./.claude/skills/hamr/
hamr add skill claude --global   # user-global:   ~/.claude/skills/hamr/
hamr add skill claude --force    # replace an existing install
```

Currently supported targets: `claude`. Support for `codex`, `opencode`, and other AI coding tools will follow.

Project-local installs must be run from the root of a HAMR project (a directory containing `hamr.toml`) and are typically committed so the whole team benefits. Global installs work from any directory and persist per-user.

---

## hamr ai capture

Capture a browser screenshot of a page for debugging, visual review, or LLM workflows.

```bash
hamr ai capture <url> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--out` | — | Output PNG path |
| `--dir` | `.hamr/captures` | Root directory for per-capture folders |
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
| `--timeout` | `15s` | Timeout for browser operations |
| `--wait` | `1s` | Extra delay after page load before capture |

```bash
hamr ai capture http://localhost:3000
hamr ai capture localhost:3000/login --text --html --json
hamr ai capture https://example.com --selector '#app' --dir .hamr/captures
hamr ai capture https://example.com/docs --full-page --wait 2s
hamr ai capture https://example.com/pricing --scroll-to middle --width 1280 --height 720
hamr ai capture https://example.com/dashboard --scroll-selector '.results-pane' --scroll-to bottom
```

By default the command creates a per-capture folder under `.hamr/captures/` and writes `screenshot.png`, optional `screenshot.txt` / `screenshot.html`, plus `meta.json`. `--out` lets you force a specific PNG path instead.

---

## hamr ai upgrade

Show scaffold changes between the project's baseline version and the current HAMR version by diffing the actual HAMR repository between version tags.

The diff is scoped to the surface a downstream project consumes or mirrors — the
`pkg/` libraries it imports, the scaffold templates its files were generated from
(`internal/cli/generator/templates/`), and the `docs/` + `llmsdocs/` the project
carries and can bring up to date. Only hamr's own internal tooling (the CLI
commands, dev server, generator logic, the `hamr` binary) and tests are
excluded — none of it ever lands in a project. This keeps the report focused on
what an upgrade must adapt to rather than the whole framework diff.

```bash
hamr ai upgrade [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as JSON |
| `--from` | — | Override the base version to diff from |
| `--applied` | `false` | Update project baseline to current HAMR version |
| `--dir` | `.hamr/ai/upgrades` | Directory to save the upgrade report |

```bash
hamr ai upgrade                    # diff between project version and current HAMR
hamr ai upgrade --json             # structured JSON output
hamr ai upgrade --from 0.1.0      # override the base version
hamr ai upgrade --applied          # bump [hamr] version in hamr.toml
```

The command clones the HAMR repository (bare, partial clone for speed) and runs `git diff` between the two version tags. The output includes a unified diff and stat summary covering all changes — scaffold templates, packages, and configuration.

Reports are saved to `.hamr/ai/upgrades/` as JSON files. An LLM agent can consume the structured output to present changes conversationally and guide the developer through what to adopt.

---

## hamr gen static

Fingerprint static assets by content-hashing filenames.

```bash
hamr gen static [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--clean` | `false` | Remove the dist dir and reset the generated manifest |

```bash
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

**Guide:** [Static Assets](09-static-assets.md) covers fingerprinting, cache headers, and deployment.

---

## hamr sync

Sync a local directory to an S3-compatible bucket.

```bash
hamr sync [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `static` | Local directory to sync |
| `--watch` | `false` | Watch for changes after initial sync |
| `--endpoint` | env `S3_ENDPOINT` | S3 endpoint URL |
| `--bucket` | env `S3_BUCKET` | S3 bucket name |
| `--region` | env `S3_REGION` | S3 region |
| `--access-key` | env `S3_ACCESS_KEY` | S3 access key |
| `--secret-key` | env `S3_SECRET_KEY` | S3 secret key |
| `--path-style` | `true` | Use path-style addressing (required for RustFS) |

```bash
hamr sync                              # one-shot sync of [static].dir to S3
hamr sync --watch                      # watch for changes and sync continuously
hamr sync --dir dist --bucket my-cdn   # sync a different directory to a specific bucket
```

S3 credentials can be provided via flags or environment variables (`S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`). Flags take precedence. When `hamr dev` is running and walked an `S3_ENDPOINT` host port off its `.env` value, `hamr sync` reads `.hamr/walks.json` automatically and connects to the walked port — no manual override needed.

**Guide:** [Sync](pkg/sync.md) covers the Go library API. [Storage](pkg/storage.md) covers S3 setup.

---

## hamr env

Print env-var rewrites derived from the running dev server's port walks. Reads `.hamr/walks.json` (written by `hamr dev` after walking) and `.env`, applies the same rewrite rules `hamr dev` uses internally, and emits the resulting `KEY=VALUE` pairs to stdout.

```bash
hamr env             # KEY=VALUE form
hamr env --export    # export KEY=VALUE form, single-quoted for safe sourcing
```

| Flag | Default | Description |
|------|---------|-------------|
| `--export` | `false` | Emit `export KEY=VALUE` for shell sourcing |
| `--dir` | `.` | Project root to resolve `.hamr/walks.json` and `.env` from (use this when invoking from a script that may not run with the project root as cwd) |

When no walks are recorded (port_walk disabled, every port was free, or `hamr dev` isn't running), the command exits 0 with no output — sourcing it is a no-op and the consumer falls through to `.env` unchanged.

```make
SHELL := bash
.SHELLFLAGS := -ec
ENV_LOAD := eval "$$(hamr env --export 2>/dev/null || true)";

migrate:
	$(ENV_LOAD) go run ./cmd/migrate
```

The scaffold's `Makefile` ships this prefix on `migrate`, `db-sh`, `generate`, and `e2e-local` already.

Match rules for which `.env` values get rewritten:

- `(localhost|127.0.0.1|0.0.0.0|[::1]):<port>` → port swapped, host kept.
- whole-value `:<port>` → port swapped (Go listener form).

Values without an explicit port (`postgres://user@localhost/db`) are not rewritten — include the port in `.env` if you want auto-walking. Remote hosts (`db.prod.example.com:5432`) are also left alone.

**Guide:** [`[dev].port_walk`](hamr-toml.md) covers the underlying mechanism.

---

## hamr lint templ

Lint `.templ` files for common issues.

```bash
hamr lint templ [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rule` | all | Run only these rules (comma-separated IDs) |
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--severity` | all | Minimum severity to report: `warning` or `error` |

```bash
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

Configure in `hamr.toml` under `[lint.templ]` — each rule mapped to `"warning"`, `"error"`, or `"off"`. Rules not listed are disabled. Unknown rule IDs and invalid severities fail loudly.

**Inline suppression.** A `templint:ignore` directive in a `//` comment silences diagnostics on the next line, or on its own line when it trails source content:

```
// templint:ignore no-native-form-actions -- manifest flow needs a browser POST
<form method="post" action={ templ.SafeURL(action) }>

<img src="logo.png">           // templint:ignore img-alt
// templint:ignore img-alt, empty-class    (comma-separated)
// templint:ignore                          (all rules)
```

Everything after ` -- ` is a human reason. Rules anchor diagnostics to the line a tag *opens* on, so for a multi-line tag the directive goes above the `<form`, not above the offending attribute. Two extra diagnostics keep directives honest: `unknown-rule` (error) for a mistyped rule ID, and `unused-suppression` (warning) for a directive that suppresses nothing. Neither is configurable; a directive naming a rule that is switched `"off"` is silently ignored.

**Guide:** [Templint](pkg/templint.md) covers rules, configuration, and library usage.

---

## hamr gen locale

Generate type-safe Go accessor methods from locale JSON files.

```bash
hamr gen locale [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--dir` | from config | Locale directory (overrides config) |
| `--out` | from config | Output file path (overrides config) |

```bash
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

---

## hamr vendor

Download and checksum frontend JavaScript dependencies.

```bash
hamr vendor [dep[@version]] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--update` | `false` | Re-download all dependencies at latest versions |
| `--verify` | `false` | Verify checksums of vendored files |
| `--url` | — | Custom URL to download |
| `--out` | — | Output path for custom URL (relative to project root) |

```bash
hamr vendor                          # vendor all deps at default/locked versions
hamr vendor htmx                     # vendor only htmx
hamr vendor alpine@3.14.9            # vendor alpine at pinned version
hamr vendor --update                 # re-vendor all at latest
hamr vendor --verify                 # check checksums
hamr vendor --url <url> --out <path> # custom dependency
```

Downloads files to `frontend/static/js/` and records checksums in `hamr.vendor.json`. Built-in deps: `htmx`, `alpine`, `idiomorph`.

---

## hamr rename-module

Rename the Go module path and rewrite all import paths.

```bash
hamr rename-module <new-module-path> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `.` | Directory containing go.mod |
| `--dry-run` | `false` | Show what would change without writing files |

```bash
hamr rename-module github.com/neworg/myproject
hamr rename-module github.com/neworg/myproject --dry-run
hamr rename-module github.com/neworg/tools --dir ./tools
```

Reads the current module path from `go.mod`, then replaces it in every `.go` and `.templ` file and the `go.mod` module directive. The `.git`, `vendor`, `node_modules`, and `testdata` directories are skipped — vendored and third-party code must not be rewritten, and `testdata` is excluded from Go builds (and often holds intentionally malformed fixtures).

---

## hamr version

Print the hamr version and commit hash.

```bash
hamr version
```

Output: `hamr <version> (<commit>)`

---

## hamr completion

Generate or install shell completion scripts for bash, zsh, or fish.

### Generate to stdout

```bash
hamr completion bash
hamr completion zsh
hamr completion fish
```

### Install

```bash
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

```bash
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

---

## Environment

`hamr sync` falls back to `.env` in the current directory for S3 credentials when both flags and shell env vars are unset. No other `hamr` command reads `.env` — the CLI deliberately doesn't mutate its own process env (earlier behavior leaked into spawned children and made `.env` edits silently invisible to live-reloaded site binaries).
