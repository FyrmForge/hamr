# `hamr.toml` — Configuration Reference

The `hamr.toml` file at the project root configures `hamr dev` (proxy, file
watchers, mocks, daemons), `hamr gen static` / `hamr gen locale`
(generators), `hamr lint templ` (linter), and records the scaffold
options chosen at `hamr new` time.

This is a schema reference. For tutorial-style usage see
[Development Workflow](02-dev-workflow.md) and
[`pkg/dev`](pkg/dev.md).

---

## `.pref.hamr.toml` — per-developer overrides

A `.pref.hamr.toml` sitting next to `hamr.toml` is merged over it at load
time, so a developer can keep local preferences out of the team's committed
config. It is optional and gitignored by the scaffold. With
`--config /elsewhere/hamr.toml` the override is looked for at
`/elsewhere/.pref.hamr.toml`.

**What it overrides:** the dev-server config — `[proxy]`, `[dev]` and every
`[dev.*]` table — as read by `hamr dev`, `hamr compose`, `hamr mcp` and
`hamr add service`, plus `[static].dir` as read by `hamr gen static` and
`hamr sync`.

**What it does not override:** `[lint.templ]` (`hamr lint templ` loads it
through `pkg/templint`), and `[hamr]` / `[options]` / `[ai]` — project
identity and scaffold metadata, which are not preferences. `hamr setup`
deliberately ignores the override too: it writes the `[dev.mcp]` values it
shows straight back into `hamr.toml`, and a local preference must not end up
in the committed file.

The merge is per-key at any depth — write only what you want to change:

```toml
# .pref.hamr.toml — everything else in hamr.toml is untouched
[proxy]
listen = ":9999"
```

Editing it while `hamr dev` is running triggers the same config reload as
editing `hamr.toml`.

**Sharp edge:** arrays of tables (`[[dev.watch]]`, `[[dev.daemon]]`,
`[[dev.docker_compose]]`) *replace* the whole list rather than merging into
it. One `[[dev.watch]]` block in the override means the override's rules are
the only watch rules. Scalar and table keys merge per-key as described above.
Watch out for this after `hamr add service`, which appends a `[[dev.watch]]`
rule to `hamr.toml`: the new rule is invisible while your override defines
any watch rules of its own.

**Second sharp edge:** the override must spell a setting the same way the
base file does. `[proxy].listen` in `hamr.toml` and `[dev].proxy_listen` in
the override are aliases for one setting, but both fields end up populated
with different values and the config fails to load with
`proxy.listen and dev.proxy_listen both set with different values`.

---

## Top-level tables

| Table                   | Used by                       | Notes |
|-------------------------|-------------------------------|-------|
| `[hamr]`                | `hamr dev` (version check)    | Scaffold metadata |
| `[options]`             | `hamr add skill` / `hamr add service` | Records `hamr new` choices |
| `[ai]`                  | `hamr ai install/upgrade`     | AI artifact directory |
| `[static]`              | `hamr gen static`             | Static asset fingerprinting |
| `[proxy]`               | `hamr dev`                    | Reverse proxy + live reload |
| `[dev]`                 | `hamr dev`                    | Watch rules, daemons, mocks |
| `[dev.email]`           | `hamr dev`                    | Mail mock at `/__hamr/mail` |
| `[dev.sms]`             | `hamr dev`                    | SMS mock at `/__hamr/sms` |
| `[dev.stripe]`          | `hamr dev`                    | Stripe mock at `/v1/*` + `/__hamr/stripe/*`, or `stripe listen` |
| `[dev.mcp]`             | `hamr dev` / `hamr mcp`       | MCP gateway for AI agents at `/__hamr/mcp/*` |
| `[dev.tunnel]`          | `hamr dev`                    | Public tunnel (cloudflared / ngrok) toggled with `T` |
| `[[dev.docker_compose]]`| `hamr dev`                    | Compose deps lifecycle |
| `[[dev.daemon]]`        | `hamr dev`                    | Long-running background processes |
| `[[dev.watch]]`         | `hamr dev`                    | File watch + build/run pipelines |
| `[lint.templ]`          | `hamr lint templ`             | Templ linter rule overrides |
| `[locale]`              | `hamr gen locale`             | i18n codegen |

---

## `[hamr]` — Scaffold metadata

| Field           | Type   | Default | Notes |
|-----------------|--------|---------|-------|
| `version`       | string | (set by `hamr new`) | Hamr CLI version that scaffolded this project. `hamr dev` (released builds only) refuses to start if the running CLI is older than this; `--skip-version-check` bypasses. Also read by `hamr ai upgrade` as the baseline for upgrade diffs. |
| `scaffolded_at` | string | (set by `hamr new`) | ISO date, informational only. |

```toml
[hamr]
version = "0.16.1"
scaffolded_at = "2026-04-24"
```

---

## `[options]` — Recorded `hamr new` choices

Informational record of what was opted into at scaffold time. Read at runtime
by `hamr add skill` (`alpine`, to tailor installed skill content) and
`hamr add service` (`database`, `db_connector`, `auth`, `locale`, so a new
service's wiring matches the project). The rest are kept as an audit trail for
humans and for tooling that may want to compare against current state.

| Field         | Type   | Values |
|---------------|--------|--------|
| `database`    | string | `"postgres"` \| `"sqlite"` |
| `db_connector`| string | `"sqlx"` \| `"gorm"` |
| `auth`        | string | `"session"` \| `"none"` |
| `css`         | string | `"plain"` \| `"tailwind"` |
| `websockets`  | bool   | |
| `e2e`         | bool   | |
| `stripe`      | bool   | |
| `locale`      | bool   | |
| `storage`     | string | `"local"` \| `"s3"` |
| `alpine`      | bool   | |

---

## `[ai]` — AI artifact directory

| Field | Type   | Default     | Notes |
|-------|--------|-------------|-------|
| `dir` | string | `.hamr/ai`  | Where `hamr ai install` writes generated skill files. |

---

## `[static]` — Static asset fingerprinting

Read by `hamr gen static`. All four fields are required when the section is
present; the scaffold sets them.

| Field      | Type   | Example | Notes |
|------------|--------|---------|-------|
| `dir`      | string | `frontend/static` | Source directory of un-fingerprinted assets. |
| `dist`     | string | `dist` | Output directory for fingerprinted copies. Current scaffolds set `frontend/dist`. |
| `manifest` | string | `internal/web/components/staticmanifest.go` | Generated Go file path. |
| `package`  | string | `components` | Package name for the generated file. |

---

## `[proxy]` — Reverse proxy + live reload

`hamr dev` only starts the proxy when this section is present. With it absent,
file watching still runs but the browser-side reload pipeline does not.

| Field           | Type   | Default          | Notes |
|-----------------|--------|------------------|-------|
| `listen`        | string | `localhost:3000` | Address the proxy listens on. Loopback by default — the dev app and the `/__hamr/*` control surface are reachable only from this machine. Set `:3000` to expose on the LAN (e.g. testing from a phone). |
| `target`        | string | `:8080`          | Where to forward requests. Must match your app's `PORT`. |
| `inject_reload` | bool   | `true`           | Inject the SSE live-reload script into HTML responses. |

Both `listen` and `target` must be `host:port` or `:port` form. Required by
`[dev.email]`, `[dev.sms]`, `[dev.stripe]`, and `[dev.mcp]` (their surfaces live on the proxy mux), and by the `[dev.tunnel]` hotkey (the tunnel forwards to the proxy).

---

## `[dev]` — Watch rules, daemons, mocks

Top-level fields apply to logging. Sub-tables (`[dev.email]`, `[dev.sms]`, `[dev.stripe]`)
and array tables (`[[dev.watch]]`, `[[dev.daemon]]`, `[[dev.docker_compose]]`)
configure the rest. At least one of watch / daemon / docker_compose must be
present.

| Field                | Type   | Default                 | Notes |
|----------------------|--------|-------------------------|-------|
| `log_file`           | string | `.hamr/dev_logs.txt`    | Rolling log file (also consumed by `hamr ai`). |
| `log_file_max_lines` | int    | `200`                   | Lines retained. Must be > 0. |
| `port_walk`          | bool   | `true`                  | When a hamr-managed port is busy, walk +1 up to a small cap and warn (see below). Set `false` to fail fast on EADDRINUSE. |
| `hamr_console_capture`| bool  | `true`                  | Pipe browser `console.*` + uncaught errors + unhandled rejections + resource-load failures + CSP violations into the dev TUI/log over `/__hamr/console` WebSocket. Set `false` to disable: the WS endpoint isn't mounted, the injected reload script doesn't patch console, and there's zero overhead. See [browser console transport](#browser-console-transport-siteconsole) below. |
| `dark_filter`        | bool   | `false`                 | Initial state of the dev panel's **Dark filter** — an `invert(1) hue-rotate(180deg)` CSS filter over the proxied site so a light-mode app is bearable to work on. Toggle it live from the dev panel (bottom-left widget); the toggle lives in the `hamr dev` process only and is never written back to `hamr.toml`. Set it in `.pref.hamr.toml` to keep it a per-developer preference. |
| `hamr_console_filter`| bool   | `false`                 | Drop browser-console frames whose message contains `[hamr]` (i.e. hamr's own injected reload-script chatter like `[hamr] page swapped`). Default `false` shows everything; flip `true` if the per-save chatter drowns out app logs. No effect when `hamr_console_capture = false`. |

#### `port_walk` — collision-tolerant port binding

By default `hamr dev` walks +1 on busy for every port it manages: the proxy
listen, the spawned-app `PORT`, and each `[[dev.docker_compose]]` service's
host-published ports. Two `hamr dev` instances on the same machine (or a
stray local Postgres on 5432) get a clean shift instead of a startup error.
Each shift logs a `WARN` so the surprise is visible.

When a port walks, the values consumers reference in `.env` (DATABASE_URL,
S3_ENDPOINT, etc.) get rewritten transparently:

- **In-process**: every spawned rule (site daemon, `hamr sync` daemon,
  custom daemons) sees the rewritten values via env injection. The `.env`
  file on disk is never modified.
- **Out of process**: `hamr dev` writes `.hamr/walks.json` after walking;
  `hamr env [--export]` reads it plus `.env` and emits the rewritten
  `KEY=VALUE` pairs for sourcing in Makefiles, shell scripts, etc. The
  scaffold's `Makefile` does this automatically via the `ENV_LOAD` prefix
  on targets that need DB/S3 env (`migrate`, `db-sh`, `generate`,
  `e2e-local`). `hamr sync` invoked standalone reads `walks.json` itself.

`.hamr/walks.json` is the contract surface between the writer (`hamr dev`)
and the readers (`hamr env`, `hamr sync`, scaffold Makefile, scaffold
`db-shell.sh`). Schema is intentionally simple — a flat `shifts` array
of `{kind, old, new}` records (compose entries also carry `service` and
`compose_name`). Regenerated on every `hamr dev` start; removed when no
walks happened so a stale file doesn't outlive its data. Gitignored under
the existing `.hamr/` rule.

Match rules for which `.env` values get rewritten:

- `(localhost|127.0.0.1|0.0.0.0|[::1]):<oldPort>` anywhere in the value
  → port swapped, host preserved (`postgres://...@localhost:5432/db` →
  `postgres://...@localhost:5433/db`).
- whole-value `:<oldPort>` (Go listener form) → port swapped
  (`LISTEN_ADDR=:8080` → `:8081`).

Values without an explicit port (`postgres://user@localhost/db` relying on
the scheme default) are **not** rewritten — include the port in `.env` if
you want auto-walking. Remote hosts (`db.prod.example.com:5432`) are also
left alone.

The whole-value `:<port>` rule rewrites *connect* and *bind* values
uniformly. For dependency ports (postgres, rustfs, etc.) this is correct
— consumers connect, hamr walked the publisher. For bind-style values
(`HEALTHCHECK_BIND=:8080`, `METRICS_ADDR=:9090`) hamr does **not** re-probe
per-var: the rewritten port could collide if multiple things in the
project happen to bind on the same number. Use distinct ports per binder.

Set `port_walk = false` to disable the shift entirely and fail fast on
EADDRINUSE — useful in CI or anywhere external tooling pins specific
ports and would mis-target if hamr silently shifted.

#### Browser console transport (`[site:console]`)

The injected reload script also pipes the browser's `window.console.*`
output, uncaught JS errors, unhandled promise rejections, resource-load
failures (`<img>`/`<script>`/`<link>` 404s), and CSP violations back to
the dev server over a WebSocket at `/__hamr/console`. They land in the
TUI main tab and `dev_logs.txt` tagged `[site:console]`, interleaved
with backend events in arrival order:

```
[site:console] hello world
[site:console] WARN deprecated API call
[site:console] ERROR TypeError: x is undefined @ app.js:42:7
[site:console] Failed to load <img> /missing.png
```

Levels render uppercase + coloured for `warn`/`error` only, matching
the existing `[hamr dev]` slog convention. Source location (`@ file:line:col`)
is appended only for uncaught errors — plain `console.*` calls leave
it off because most don't carry useful frame info.

**Limitations.** Browser-engine warnings printed directly to DevTools
(deprecation notices, mixed-content, autoplay blocked, the formatted
"Failed to load resource" lines) aren't observable from page JS, so they
can't be captured. `fetch`/`XHR` network errors aren't wrapped either.

**Disable entirely.** Set `hamr_console_capture = false` to skip the
whole transport: the server doesn't mount the WS endpoint, and the
injected reload script (which reads the flag from the SSE config
payload) skips console patching, the error/rejection/CSP listeners,
and the WS connection. Default `true`.

> Toggling this *while a tab is open* applies on the next page reload —
> a browser that already started capture keeps trying to reach the
> (now-removed) WS endpoint with reconnect backoff until you refresh.

**Filtering.** Set `hamr_console_filter = true` in `[dev]` to drop
frames whose message *contains* `[hamr]` anywhere in the text — this
catches the reload script's own `[hamr] live reload connected`,
`[hamr] page swapped`, `[hamr] CSS reloaded`, etc. Default `false`
keeps them — the duplication during normal use is mild and the
visibility when hamr's own JS misbehaves is high-value. Substring
match is intentional; if app code legitimately logs the literal
string `[hamr]` it will be dropped too.

### `[dev.email]` — Mail mock

When `enabled = true`, an inbox is mounted at `/__hamr/mail` on the proxy.
Pair with `pkg/emailmock` as your `email.Sender` impl in dev. Requires
`[proxy]`. See [`pkg/emailmock`](pkg/emailmock.md).

| Field               | Type   | Default                    | Notes |
|---------------------|--------|----------------------------|-------|
| `enabled`           | bool   | `false`                    | |
| `max_messages`      | int    | `500`                      | Ring buffer cap; oldest evicted. |
| `max_message_bytes` | int    | `10485760` (10 MiB)        | Per-message size cap. |
| `persist`           | bool   | `true`                     | Mirror to mbox file. `false` = in-memory only. |
| `persist_path`      | string | `.hamr/mail/inbox.mbox`    | mbox file path. |

### `[dev.sms]` — SMS mock

When `enabled = true`, an inbox is mounted at `/__hamr/sms` on the proxy.
Pair with `pkg/smsmock` as your `sms.Sender` impl in dev. Requires
`[proxy]`. See [`pkg/smsmock`](pkg/smsmock.md).

| Field          | Type   | Default                   | Notes |
|----------------|--------|---------------------------|-------|
| `enabled`      | bool   | `false`                   | |
| `max_messages` | int    | `500`                     | Ring buffer cap; oldest evicted. |
| `persist`      | bool   | `true`                    | Mirror to JSONL file. `false` = in-memory only. |
| `persist_path` | string | `.hamr/sms/inbox.jsonl`   | JSONL file path. |

### `[dev.stripe]` — Stripe mock or `stripe listen`

`mode` picks how `hamr dev` handles Stripe. Both modes need `[proxy]`.

- **`mock`**: a Stripe-compatible HTTP backend mounts at `/v1/*` and `/v2/*`,
  and a dashboard at `/__hamr/stripe/*` on the proxy. Apps point real
  `stripe-go` at the proxy URL via `STRIPE_MOCK=true`. See
  [`pkg/stripemock`](pkg/stripemock.md).
- **`listen`**: `hamr dev` runs `stripe listen` against your sandbox, using
  `STRIPE_KEY` from `.env`, and forwards to the URLs below. The app talks to
  api.stripe.com. Needs the [Stripe CLI](https://docs.stripe.com/stripe-cli)
  on `PATH`. Live keys (`sk_live_`, `rk_live_`) are refused.

Press `S` to flip between them at runtime. `dev.stripe.enabled` was replaced
by `mode`; a config that still sets it fails to load.

| Field            | Type   | Default                     | Notes |
|------------------|--------|-----------------------------|-------|
| `mode`           | string | `"off"`                     | `"off"`, `"mock"` or `"listen"`. |
| `webhook_url`    | string | (required unless off)       | Where signed v1 snapshot events go, e.g. `http://localhost:8080/api/webhooks/stripe`. |
| `thin_webhook_url` | string | empty                     | Where v2 thin events (Accounts v2 changes) go, e.g. `http://localhost:8080/api/webhooks/stripe/v2`. Empty = thin events are not delivered. |
| `connect_webhook_url` | string | empty                  | Events on a connected account. Empty = `webhook_url`. |
| `thin_connect_webhook_url` | string | empty             | Thin events on a connected account. Empty = `thin_webhook_url`. |
| `thin_events`    | list   | the two `v2.core.account[...]` events | Thin events `stripe listen` forwards (it has no default). |
| `webhook_secret` | string | (required unless off)       | The mock's signing secret. Also used in listen mode if the listener can't start and hamr falls back to the mock. |
| `persist`        | bool   | `true`                      | Mock only. Persist state across `hamr dev` restarts. |
| `persist_path`   | string | `.hamr/stripe/state.json`   | Mock only. State file path. |

All four URLs follow the app's port when `hamr dev` walks to a free one.

**Env injected into your app** (overrides `.env`, like the port-walk rewrites):

| Var | mock | listen |
|---|---|---|
| `STRIPE_MOCK` | `true` | `false` |
| `STRIPE_KEY` | not injected | from `.env` (what the listener uses) |
| `STRIPE_WEBHOOK_SECRET`, `_V2`, `_CONNECT`, `_V2_CONNECT` | `webhook_secret` | the secret `stripe listen` prints |
| `HAMR_STRIPE_MOCK_URL` | proxy URL | proxy URL |

Locally one secret signs every kind of event, so all four secret vars hold the
same value.

**On `S` (or at boot with `mode = "listen"`):** hamr starts `stripe listen`
with the key in its environment (not on the command line), waits up to 30s
for the signing secret, injects the env and restarts your running apps. If it
fails (no CLI, bad key, no secret), a flip leaves the current mode running and
boot falls back to the mock; the log says why. If `stripe listen` exits on its
own, hamr switches back to the mock. Changing `STRIPE_KEY` in `.env` while
listening restarts the listener. Your app restarts on the `.env` edit with the
new key straight away, then once more when the new listener's secret is in.

In listen mode the mock's dashboards and state stay up, but it won't deliver
webhooks (the app holds the listener's secret, so they'd fail verification)
and the MCP `stripe.*` tools error — except `stripe.mode`.

### `[dev.mcp]` — MCP gateway for AI agents

When `enabled = true`, `hamr dev` exposes a token-gated MCP gateway at
`/__hamr/mcp/*` on the proxy. An agent (Claude Code, Codex, opencode) spawns
the `hamr mcp` bridge, which forwards tool calls to the gateway authenticated by
a per-run token written to `.hamr/dev.json` (mode 0600). Requires `[proxy]`.
See [`hamr mcp`](cli.md) and the security notes below.

| Field          | Type              | Default               | Notes |
|----------------|-------------------|-----------------------|-------|
| `enabled`      | bool              | `false`               | Initial state at launch. Toggle at runtime with `M` in the TUI (doesn't rewrite this). |
| `access`       | table             | (none → zero tools)   | Per-area grant: `read`, `write`, or omit (deny). `write` implies `read`. |
| `make_targets` | list of string    | (empty → all allowed) | Restrict `make.run` to named targets — keep e.g. `make deploy` out of reach. |
| `make_wait`    | duration          | `20s`                 | How long `make.run` waits before returning "still running, poll logs". |
| `log_file`     | string            | `.hamr/mcp_logs.txt`  | Audit log of every agent tool call. `"none"` disables. |

**Access areas** (each grants `read` / `write`):

| Area     | `read` exposes                | `write` adds                          |
|----------|-------------------------------|---------------------------------------|
| `dev`    | `dev.info`                    | `dev.restart`                         |
| `logs`   | `logs.read`, `console.read`, `http.read` | —                          |
| `docker` | `docker.logs`, `docker.status`| `docker.restart`, `docker.wipe`       |
| `mail`   | `mail.list`, `mail.get`       | `mail.clear`, `mail.ingest`           |
| `sms`    | `sms.list`, `sms.get`         | `sms.clear`, `sms.ingest`             |
| `build`  | — (write-only)                | `rule.run`, `rebuild.all`, `make.run` |
| `stripe` | `stripe.list`                 | `stripe.complete`, `stripe.expire`, `stripe.refund`, `stripe.mode` |

```toml
[dev.mcp]
enabled = true

[dev.mcp.access]
docker = "read"     # logs/status, not restart/wipe
build  = "write"
mail   = "write"
```

**Observability tools.** `logs.read` returns ANSI-stripped, timestamped lines
and prefix-matches rule names (`rule="site"` catches `site:build`/`site:run`).
`console.read` returns structured, timestamped browser-console frames
(`{time, level, msg, src}`). `http.read` exposes the dev proxy's own request log
(`{time, method, path, status, durationMs}`) — including `/__hamr/*`, static
assets, and SSE/WS that the app's access log never sees — filterable by
`method`/`path`/`min_status`; handy for verifying HTMX request/response flows.

**Async + waits.** `docker.restart`/`docker.wipe` dispatch and return `{ok}` by
default; pass `wait: true` (+ optional `wait_timeout`, default 60s) to block
until containers are running/healthy and get the final statuses back. `make.run`
returns inline `output` for fast targets, else `status:"running"` (poll
`logs.read`).

**Security.** Default-off and default-deny. The gateway is localhost-only (the
proxy binds loopback by default), token-gated (per run, in a 0600 gitignored
file), permission-enforced per call, and has a runtime kill-switch (`M`).
Granting `build` lets the agent run any Makefile target unless `make_targets`
constrains it. Residual risk: any local process running as you can read the
token while the gateway is on — acceptable on a single-user dev box.

### `[dev.tunnel]` — public tunnel

Press `T` in the `hamr dev` TUI to put the dev proxy on the internet through a
locally installed tunnel binary — for webhooks, OAuth callbacks, or showing
someone a page. The tunnel is never on at startup and never survives `R` or a
config reload; press `T` again to turn it off. Requires `[proxy]`. The whole
table is optional: with no `[dev.tunnel]`, `T` runs `cloudflared` with the
defaults below.

| Field      | Type           | Default         | Notes |
|------------|----------------|-----------------|-------|
| `provider` | string         | `"cloudflared"` | `"cloudflared"` (no account; new random `*.trycloudflare.com` URL on every start) or `"ngrok"` (needs `ngrok config add-authtoken`). |
| `args`     | list of string | `[]`            | Extra flags appended to the built-in command, e.g. `["--url", "me.ngrok-free.app"]` for a fixed ngrok domain. |
| `cmd`      | string         | (none)          | Custom shell command replacing `provider` + `args`. Must contain `{port}` (the local port to forward). The first `https://` URL it prints is used. |
| `env`      | list of string | `["BASE_URL"]`  | Vars set to the public URL for every process `hamr dev` spawns. `[]` sets nothing. |

Built-in commands (`<port>` is a loopback listener hamr picks):

- cloudflared: `cloudflared tunnel --no-autoupdate --url http://127.0.0.1:<port> <args>`
- ngrok: `ngrok http 127.0.0.1:<port> --log stdout --log-format logfmt <args>`

```toml
[dev.tunnel]
provider = "ngrok"
args = ["--url", "me.ngrok-free.app"]
env = ["BASE_URL"]

# or anything else that prints its public URL:
# cmd = "ssh -R 80:localhost:{port} serveo.net"
```

**On `T`:** hamr binds the tunnel listener, starts the command, and waits up to
15s for its URL (output shows in the hamr tab as `[tunnel]`; a failure logs the
output tail). Then it sets each `env` var to the URL and restarts every running
`run` rule and daemon so they pick it up — builds are not re-run, and a rule
whose build failed stays down. **Off** (or the tunnel process dying) removes
the vars and restarts the same processes. The status ticker shows
`tunnel starting` / `tunnel stopping` meanwhile; while on, the public URL
replaces the localhost URL in the status bar and `o` opens it.

**Security.** The tunnel serves the same proxy — live reload, error pages, and
the mail/SMS/Stripe mocks all work through it — on its own listener that
allows only `/__hamr/reload`, `/__hamr/logo.png`, `/__hamr/mail/*`,
`/__hamr/sms/*` and `/__hamr/stripe/*`. Every other `/__hamr/*` route returns
403: the ones that run commands, leak logs, accept log lines or flip the dark
filter for every open browser, and any route added later. Process output is withheld too: the live-reload stream
sends tunnel visitors no `output` lines, a config with rule/daemon/stack names
only (no commands, globs or files), and build errors without their output; the
build error page names the failing rule but hides its output. Everything else
is public to anyone holding the URL, with no password: your app, the mock
inboxes, and the Stripe mock API at `/v1/*`. Turn it off when you're done.

Anyone you share the URL with also gets the injected hamr dev overlay (so rule
and compose-stack names are visible); its rebuild, docker and dark-filter buttons
return 403.

An ngrok fixed domain is per-developer: put `[dev.tunnel]` in
`.pref.hamr.toml` rather than the committed `hamr.toml`.

The Stripe mock still hands out `localhost` checkout URLs (its base URL is the
local proxy), so mock checkout flows only work from your own browser.

### `[[dev.docker_compose]]` — Compose deps

Each entry brings up a compose file before watch rules run.

| Field          | Type     | Notes |
|----------------|----------|-------|
| `name`         | string   | Required. Must be unique across all `dev.*` entries. |
| `file`         | string   | Required. Path to `docker-compose.yaml`. |
| `services`     | []string | Optional subset; defaults to all services in the file. |
| `keep_running` | bool     | `true` = leave running on `hamr dev` exit. Default `false`. |
| `wait_ready`   | bool     | Block until services pass healthcheck before continuing. |
| `env`          | []string | `KEY=value` pairs. |

### `[[dev.daemon]]` — Background processes

Long-running processes started once at launch. No file watching, no restart.

| Field  | Type     | Notes |
|--------|----------|-------|
| `name` | string   | Required. Must be unique. |
| `cmd`  | string   | Required. Shell command. |
| `dir`  | string   | Working directory for `cmd`, relative to the project root. Defaults to the project root. |
| `env`  | []string | `KEY=value` pairs. |

### `[[dev.watch]]` — Build/run pipelines

Each rule watches files, runs `cmd` on change, and optionally keeps `run`
alive between builds. Rules can depend on each other to form a build graph.
Any write, create, delete or rename counts as a change, and so does a
timestamp-only one: `touch .env` reruns a rule that watches `.env`. A
`chmod` or `chown` that leaves the timestamp alone does not, so a rule can
`chmod +x` files in its own watch set without retriggering itself.

| Field      | Type            | Default | Notes |
|------------|-----------------|---------|-------|
| `name`     | string          |         | Required. Unique across all `dev.*` entries. |
| `watch`    | string\|[]string|         | Required. Glob patterns relative to project root. |
| `ignore`   | string\|[]string|         | Glob patterns to exclude. |
| `cmd`      | string          |         | One-shot build command. Required if `run` is unset. |
| `run`      | string          |         | Long-running process; restarted after each successful `cmd`. |
| `dir`      | string          |         | Working directory for `cmd` and `run`, relative to the project root. Defaults to the project root. Does **not** affect `watch`/`ignore`. |
| `depends`  | []string        |         | Other watch-rule names that must succeed first. |
| `debounce` | int (ms) or string | `100`ms | E.g. `200` or `"200ms"`. |
| `reload`   | string or bool  | `"none"`| `"full"` / `"css"` / `"none"`. `true`/`false` = `full`/`none`. |
| `env`      | []string        |         | `KEY=value` pairs for `cmd` and `run`. |

Cycles in `depends` fail validation at startup. Unknown deps fail too.

`dir` must exist, must be relative, and must resolve inside the project root
(symlinks included) — otherwise startup fails. `watch` and `ignore` globs are
always project-root-relative, so a rule can watch the whole repo while building
in a subdirectory:

```toml
[[dev.watch]]
name = "tailwind"
watch = ["**/*.templ", "frontend/css/input.css"]   # root-relative
dir = "frontend"
cmd = "npm run css:build"                          # runs inside frontend/
```

```toml
[[dev.watch]]
name = "site"
watch = ["**/*.go", "**/*.templ", ".env"]
ignore = ["*_templ.go", "*_test.go"]
cmd = "go build -o ./bin/site ./cmd/site"
run = "./bin/site"
depends = ["templ"]
reload = "full"
```

---

## `[lint.templ]` — Templ linter rules

Read by `hamr lint templ` and the `templ` watch rule.

Each entry is a rule ID mapped to one of `"warning"`, `"error"`, or `"off"`.
Rules **not listed** are disabled; `"off"` is equivalent to omitting the rule.
Unknown rule IDs and invalid severities cause `hamr lint templ` to fail.

The scaffold writes a `[lint.templ]` block listing every rule at its
recommended severity, so a freshly generated project starts fully linted.

**Recommended defaults (what the scaffold writes):**

| Rule                     | Default  | Checks |
|--------------------------|----------|--------|
| `inline-if`              | error    | `if` must use braces, not inline `@` syntax |
| `inline-for`             | error    | `for` must use braces, not inline `@` syntax |
| `inline-switch`          | error    | `switch` must use braces, not inline `@` syntax |
| `no-native-form-actions` | error    | `action=`, `method=`, `formaction=` flagged (use htmx) |
| `htmx-conflict`          | error    | Same element with `hx-*` and a native form attribute |
| `img-alt`                | warning  | `<img>` tags require `alt` attribute |
| `no-href`                | warning  | `<a>` tags require `href` attribute |
| `inline-style`           | warning  | `style="..."` attributes flagged |
| `empty-class`            | warning  | `class=""` flagged |
| `js-href`                | warning  | `href="javascript:..."` flagged |

```toml
[lint.templ]
inline-if              = "error"
no-native-form-actions = "error"
htmx-conflict          = "error"
img-alt                = "error"     # promote a warning to error
inline-style           = "off"       # disable the rule
# rules omitted entirely are also disabled
```

---

## `[locale]` — i18n codegen

Read by `hamr gen locale`. Only present when scaffolded with `--locale`.

| Field     | Type   | Example                         | Notes |
|-----------|--------|---------------------------------|-------|
| `default` | string | `"en"`                          | Fallback locale when a key is missing in the requested one. |
| `dir`     | string | `"locales"`                     | Directory of `<lang>.json` files. |
| `output`  | string | `"internal/locale/locale.go"`   | Generated Go file path. |
| `package` | string | `"locale"`                      | Package name for the generated file. |

---

## Common patterns

**Multi-service: web + API.** Two `[[dev.watch]]` rules with non-overlapping
`watch` and `ignore` patterns. Only the web service goes through the proxy;
the API rule sets `reload = "none"`.

**S3 sync of fingerprinted assets.** Add a `[[dev.daemon]]` running
`hamr sync --watch --bucket my-bucket-static`.

**Tailwind.** Add a `[[dev.watch]]` with `dir = "frontend"` running
`npm run css:build` on `.templ` changes. Scaffold the project with
`--css tailwind` to get this wired automatically.

For worked examples of each, see [Dev Server: Examples](pkg/dev.md#examples).
