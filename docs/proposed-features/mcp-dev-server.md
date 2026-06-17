# Proposed: MCP server for `hamr dev`

Status: **implemented** (including the dedicated MCP TUI tab — it sits last in
the `Tab` cycle and shows one line per agent request, fed by the same stream as
`.hamr/mcp_logs.txt`). This doc is the design of record.

Let an AI agent perform the same actions a developer can from the `hamr dev`
TUI and browser injection menu: read/query logs, run/reload watch targets,
run Makefile targets, control docker compose stacks, and drive the mail/stripe
mocks.

## Decisions locked (from discussion)

- **Transport — stdio bridge (design B).** A new `hamr mcp` subcommand is the
  MCP server the agent spawns. `hamr dev` does **not** host a network MCP
  listener. The bridge talks to the running dev server over its existing
  localhost HTTP API (`/__hamr/*`).
- **Secret handshake via runtime file.** `hamr dev` writes `.hamr/dev.json`
  (mode 0600, already gitignored): `{ proxyURL, token (fresh per-run) }`.
  `hamr mcp` reads it and authenticates to localhost. The agent's MCP config
  holds **no secret** — just `{"command":"hamr","args":["mcp"]}`.
- **Multi-instance resolution.** Several `hamr dev`s can run at once (one per
  project). The bridge resolves *which* project via, in order: (1) an explicit
  `hamr mcp --project <path>` flag, else (2) walking **up from cwd** (git-style)
  to the nearest `.hamr/dev.json`. No global registry. The install helper bakes
  `--project <abs>` **only** for Codex (global config, cwd = launch dir, not
  guaranteed); Claude/opencode configs are project-scoped so they rely on cwd
  and stay portable (no absolute path baked into a possibly-shared file).
- **Staleness handling.** `hamr dev` writes the file fresh (new token) on start
  and removes it on clean shutdown. The bridge validates liveness (connect +
  auth) per call and returns a clear "dev not running / restart it" error
  rather than hanging on a dead port from a crashed instance.
- **Toggle — `[dev.mcp] enabled`, default OFF.** Opt-in. Not written into the
  scaffold template; documented only.
- **Permissions — per-area access level, default-deny.** Each functional area
  (`dev`, `logs`, `docker`, `mail`, `build`) is granted `read`, `write`, or
  (by omission) `deny`. `write` implies `read`. A tool whose area isn't granted
  is not exposed at all. Per-tool granularity is intentionally **not** offered
  — restart-yes-wipe-no is a niche split and the area model is far easier to read.
- **Binding — proxy defaults to localhost.** Changes the current `:3000`
  (all-interfaces) default. LAN/phone testing becomes an explicit opt-in by
  setting `proxy_listen` back to `:3000`.
- **TUI runtime kill-switch.** An indicator shows gateway state; a hotkey
  flips it on/off for the session without touching `hamr.toml`. The access map
  still comes from config — the toggle is a master switch, not a permission editor.

## Open decisions

1. ~~MCP Go dependency~~ — **resolved: official `github.com/modelcontextprotocol/go-sdk`**
   (new dependency, approved in discussion per CLAUDE.md).
2. ~~TUI hotkey letter~~ — **resolved: `M` (shift+m)**, reads as "MCP" beside
   lowercase `m` (makefile menu). `ctrl+m` was rejected — it's identical to
   `enter` at the terminal level and would clash with the existing binding.

## Config surface (`hamr.toml`)

```toml
[dev.mcp]
enabled      = true              # default false; runtime toggle overrides per-session
make_targets = ["ai", "test"]    # optional; if omitted, ALL Makefile targets allowed
make_wait    = "20s"             # optional; bounded wait before make.run returns "check back later"
log_file     = ".hamr/mcp_logs.txt"  # optional; audit log of agent tool calls; "none" disables

[dev.mcp.access]          # default-deny; any unlisted area = deny; write implies read
docker = "read"           # logs/status, but not restart/wipe
build  = "write"
mail   = "write"
```

`make_targets` constrains `make.run`. **Absent = every target allowed** (the
permissive default); set it to lock the agent to a named subset (e.g. keep
`make deploy` out of reach).

Each area grants `read`, `write`, or (by omission) `deny`. The tools each
level exposes:

| area     | `read` exposes        | `write` adds (⊇ read)   |
|----------|-----------------------|-------------------------|
| `dev`    | `dev.info`            | —                       |
| `logs`   | `logs.read`, `console.read` | —                 |
| `docker` | `docker.logs`, `docker.status` | `docker.restart`, `docker.wipe` |
| `mail`   | `mail.list`, `mail.get` | `mail.clear`, `mail.ingest` |
| `build`  | — (write-only area)   | `rule.run`, `rebuild.all`, `make.run` |
| `stripe` | `stripe.list`         | `stripe.complete`, `stripe.expire`, `stripe.refund` |

Write-only areas (`build`) treat `read` as `deny` — there are no read-only
tools to expose. With no `[dev.mcp.access]` table at all, zero tools are
exposed; the TUI indicator flags "on, 0 tools".

## Action / tool inventory

Each allowed action becomes one MCP tool. Mapping to the existing surface:

| MCP tool       | Backed by                                  | Today's path                | New plumbing? |
|----------------|--------------------------------------------|-----------------------------|---------------|
| `logs.read`    | `LogBuffer.Lines()`                        | `GET /__hamr/logs`          | no            |
| `docker.logs`  | `dockerLogs`                               | `GET /__hamr/docker/{n}/logs` | no          |
| `docker.status`| `dockerStatus`                             | `GET /__hamr/docker/{n}/status` | no        |
| `docker.restart`| `dockerRestart`                           | `POST /__hamr/docker/{n}/restart` | no      |
| `docker.wipe`  | `dockerWipe`                               | `POST /__hamr/docker/{n}/wipe` | no         |
| `console.read` | `.hamr/dev_logs.txt` (console-tagged lines) | none — needs token-gated file-tail endpoint | **yes — small endpoint** |
| `rule.run`     | `requestRun` (serialized scheduler)        | `POST /__hamr/rule/{n}/run` | no            |
| `mail.*`       | `MailMock`                                 | `/__hamr/mail/*`            | no            |
| `rebuild.all`  | `RebuildAll` (TUI-only today)              | none                        | **yes — add HTTP action** |
| `make.run`     | `exec.Command("make", t)` in TUI model     | none                        | **yes — see below** |

(`stripe.*` — `stripe.list` reads mock state; `stripe.complete`/`expire`/`refund`
call `StripeMock` methods extracted from the dashboard handlers. See below.)

### The two parity gaps

- **`rebuild.all`** already exists as `DevActions.RebuildAll()` but is only
  reachable from the TUI hotkey. Add a thin HTTP route
  (`POST /__hamr/rebuild`) that calls it. Low risk — reuses `requestRun`.
- **`make.run`** is the awkward one. Today it runs `exec.Command("make", target)`
  directly inside the bubbletea model (`tui/model.go` `dispatchRun`), not
  through the action layer or scheduler. Options:
  - **(a)** Add a server-side make-run action in `DevActions` that runs the
    target via `ProcessManager.RunCommand` and streams into the LogBuffer.
    Clean, but output shows in the log pane, **not** the TUI's floating run-box.
  - **(b)** Refactor `dispatchRun` so both the TUI hotkey and the new action
    funnel through one shared make-runner. More work; keeps UX consistent.
  - Recommendation: **(a)** for v1 — simplest, and an agent doesn't need the
    floating box. Note the divergence in docs.

## Tool definitions (v1)

Resolved: official SDK, make-run option (a), no access table = zero tools.

Naming: `area.verb`. Each tool below lists inputs → output. Outputs are JSON.
Tools are grouped by the area + level (`read`/`write`) that exposes them — see
the access table above.

### Discovery

- **`dev.info`** — _(read)_ the controllable surface so the agent can discover
  valid names before acting.
  - inputs: none
  - output: `{ proxyURL, appPort, rules:[{name,status}…], stacks:[{name,services:[…],status}…], makeTargets:[…], errors:[{source,message}…], mail:{…}, stripe:{…}, gateway:{enabled,access:{…},tools:[…]} }`
  - **Ports/URLs reflect resolved runtime values** (post port-walk), not the
    originally-configured `hamr.toml` values.
  - `makeTargets` reflects what the agent may actually run — the Makefile
    targets intersected with `make_targets` when that allowlist is set.
  - Exposed by `dev = "read"`. Consequence: an access map granting `build` but
    not `dev` leaves the agent unable to discover rule/target names via this
    tool (it still sees its native tool list). Accepted — `dev = "read"` is in
    nearly every realistic config.
  - Permissions are **embedded** here (`gateway` field), not a separate tool —
    the MCP native tool list already conveys effective permissions; `gateway`
    is a convenience echo for reasoning/debugging.

### Read

- **`logs.read`** — query the process-output ring buffer (max 1000 lines).
  - inputs: `rule?` (filter to one watch rule), `contains?` (substring on text), `tail?` (last N matches, default 200)
  - output: `[{rule, text}…]`
- **`docker.logs`** — recent compose logs for a stack, filterable.
  - inputs: `name` (stack), `service?` (one service; default all), `tail?` (default 100), `since?` (compose duration e.g. `"5m"`), `contains?` (substring filter, applied after fetch)
  - output: `{output: string}`
  - note: `service`/`tail`/`since` map to native `docker compose logs` flags; `contains` is filtered server-side.
- **`docker.status`** — container states for a stack, filterable.
  - inputs: `name` (stack), `service?` (one service; default all)
  - output: `[{service, state, health, status}…]`
- **`console.read`** — browser console frames. No new *state* needed (frames
  are already persisted, tagged + ANSI-stripped, in `.hamr/dev_logs.txt` via
  `devOut = MultiWriter(terminal, fileLog)`), but it **does** need a new
  **token-gated** `GET /__hamr/logs/console` endpoint on the dev server that
  tails+filters that file. The bridge must **not** read the file directly —
  that would bypass the gateway/kill-switch (see security note below).
  - inputs: `level?` (substring match on the rendered level tag), `tail?`, `contains?`
  - output: `[{text}…]` (rendered text lines, not the original `{level,msg,src}` struct — the file is post-render)

### Build

- **`rule.run`** — run/reload one watch rule (enqueued onto the serialized scheduler).
  - inputs: `name` (rule)
  - output: `{ok: true}` (enqueued; async — poll `logs.read`)
- **`rebuild.all`** — enqueue every watch rule.
  - inputs: none
  - output: `{ok: true}`
- **`make.run`** — run a Makefile target via the new server-side action (a).
  **Bounded-wait hybrid:** dispatches the target to run in the background
  (output streaming to the LogBuffer), waits up to `make_wait` (config knob,
  default ~20–30s), then:
  - completes in time → `{status:"done", exitCode, output}` (feels synchronous
    for fast targets);
  - exceeds the wait → `{status:"running", message:"poll logs.read"}`; the
    process keeps running server-side.
  - inputs: `target` — rejected with an error if `make_targets` is set and the
    target isn't in it; any target allowed when `make_targets` is absent.
  - output: see above
  - **Completion marker:** on exit the runner appends a marker line to the log
    (e.g. `[make:<target>] exited <code>`) so the agent can detect done + exit
    code via `logs.read` after a "check back later". Without it, polling shows
    output but no clear done signal.

### Docker (destructive)

- **`docker.restart`** — restart a stack or one service.
  - inputs: `name` (stack), `service?`
  - output: `{ok: true}` (dispatched async — poll `docker.status`)
- **`docker.wipe`** — `down -v` + `up` (drops volumes).
  - inputs: `name` (stack), `service?`
  - output: `{ok: true}` (dispatched async)

### Mail (mock state)

- **`mail.list`** — inbox summaries. inputs: none → `[{id, from, to, subject, date}…]`
- **`mail.get`** — one message. inputs: `id` → `{headers, text, html}`
- **`mail.clear`** — empty inbox. inputs: none → `{ok: true}`
- **`mail.ingest`** — inject a message. inputs: `{From, To, Subject, Text, HTML}` (From/To accept a plain email string) → `{id}`

### Stripe (in v1)

The stripe lifecycle ops lived only inside HTML dashboard handlers. They were
extracted into callable `StripeMock` methods (`completeCheckout`,
`expireSession`, `refundPayment`, `stateSummary`) that the dashboard handlers
now also call — single source of truth — and exposed as tools:

- **`stripe.list`** _(read)_ — state snapshot: sessions, payment intents,
  payouts, refunds, accounts (id/status/amount). → `StripeStateSummary`.
- **`stripe.complete`** _(write)_ — apply an outcome to a checkout session.
  inputs: `session`, `outcome` (paid|failed|cancelled).
- **`stripe.expire`** _(write)_ — expire an open session. inputs: `session`.
- **`stripe.refund`** _(write)_ — refund a payment intent. inputs:
  `payment_intent`, `amount`, `reverse_transfer?`, `refund_application_fee?`.

Niche Connect/onboarding ops (payout-complete, account-complete, PI-complete,
resend) are not yet exposed — they can follow the same extraction pattern.

### Async caveat

`docker.restart`/`docker.wipe` and `rule.run`/`rebuild.all` dispatch and return
immediately (matching today's handlers). `make.run` is bounded-wait: synchronous
result for fast targets, "check back later" + poll for slow ones. Tool
descriptions state this so the agent knows to poll `docker.status` / `logs.read`
for completion rather than assuming the ack means "done".

## Agent connection + install helper

Two hops: **agent ⟷ `hamr mcp`** over stdio (MCP protocol), then **`hamr mcp`
⟷ `hamr dev`** over localhost HTTP (`/__hamr/*`). The agent never holds the
port or token — the bridge discovers both from `.hamr/dev.json` at call time.
This late binding is why the agent-facing transport is stdio, not HTTP: it
survives port-walk and per-run token rotation with a permanent, secret-free
client config.

The MCP server (`hamr mcp`) is a standard stdio server — works with any
compliant client. Only client-side *registration* differs. v1 ships an
install helper for three clients, each a different shape **and** location:

| client    | file                                   | scope   | shape |
|-----------|----------------------------------------|---------|-------|
| Claude    | `.mcp.json`                            | project | `{"mcpServers":{"hamr-dev":{"command":"hamr","args":["mcp"]}}}` |
| Codex     | `~/.codex/config.toml`                 | global  | `[mcp_servers.hamr-dev]` / `command="hamr"` / `args=["mcp"]` |
| opencode  | `opencode.json` (or `~/.config/opencode/opencode.json`) | project (or global) | `{"mcp":{"hamr-dev":{"type":"local","command":["hamr","mcp"],"enabled":true}}}` |

(opencode shape to re-verify against current docs at build time.)

`hamr mcp install --client claude|codex|opencode`. Requirements:

- **Merge, never clobber** existing entries. JSON targets (Claude, opencode):
  parse-merge-write. **Codex (TOML):** the user's hand-maintained global file —
  re-encoding via BurntSushi strips comments/ordering, so surgically
  append/replace only the `[mcp_servers.hamr-dev]` block as text.
- **`--dry-run`** to preview the write.
- **Idempotent** — re-running updates, never duplicates.
- Binary reference: `"hamr"` (assumes PATH). Document the PATH requirement.

### Open confirms on the helper

- Binary as `"hamr"` (PATH) vs absolute `os.Executable()` path — **lean PATH**.
- Bare `hamr mcp install` (no `--client`): auto-detect & install all three
  found, or error asking for `--client`? **Lean auto-detect.**

## Components to build / change

1. **`internal/devserver/config.go`** — add `MCP` struct to `Dev`
   (`enabled bool`, `access map[string]string`, `make_targets []string`,
   `make_wait duration`), defaults, validation (unknown area or level = config
   error; `read` on a write-only area allowed but exposes nothing), an
   area+level → tool-set resolver.
2a. **MCP audit log** (`internal/devserver/`) — a dedicated `.hamr/mcp_logs.txt`
   (config `log_file`, `"none"` disables, capped) written via the existing
   `rollingFileWriter`. Written **server-side at the token-auth choke point** so
   the record is complete regardless of bridge behavior. One line per tool call:
   `<ts> [mcp] <tool> <key args> → <outcome>`. Audit only — full streamed output
   stays in the main log, not duplicated here. Logs reads too (what the agent
   inspected), not just writes. This is the canonical "what did the agent do"
   record; the main-log `[mcp]` attribution (below) is the live-glance version.
2. **Gateway + token** (`internal/devserver/mcpgateway.go`) — **dedicated
   `POST /__hamr/mcp/{tool}` namespace**, not middleware retrofitted onto the
   browser routes. (Discovered during build: `handleInboxOrDetail` serves both
   HTML and JSON on the same `/__hamr/mail` path and the GET reads are
   unguarded — retrofitting auth there means content-negotiation-dependent auth
   and breaking direct browser navigation. A separate namespace sidesteps all
   of it.) Properties:
   - **Token-only auth** — browsers never hit `/__hamr/mcp/*` (they use the
     original routes), so auth is simply: valid bearer token AND gateway-enabled.
   - **Per-call `ToolAllowed(tool)`** is the security boundary (the bridge's
     tool-list filtering is only UX) — a token holder can craft any request, so
     the gateway checks the access map on every dispatch.
   - **Kill-switch gates exactly this namespace** — "off" → all `/__hamr/mcp/*`
     rejected; the browser menu is unaffected by construction (different routes).
   - On `Run()`: generate token, construct the gateway whenever the proxy is up
     (so the runtime toggle can flip it on), and — when enabled — write
     `.hamr/dev.json` (0600) + open the audit log; remove/close on shutdown.
   - The dispatch table calls existing in-package code directly (`logBuf.Lines`,
     `dockerLogsOpts`, `dockerStatus`, `restart`/`wipe`, `requestRun`,
     `RebuildAll`, the make-run + console-tail helpers) — so `rebuild.all`,
     `make.run`, and `console.read` are **dispatch entries, not new HTTP routes**
     (this absorbs former component #4).
3. **`internal/cli/cmd/mcp.go`** — new `hamr mcp` cobra subcommand. Walks up
   from cwd to the nearest `.hamr/dev.json`, opens an MCP stdio server (official
   SDK). Advertises the tool set from **`hamr.toml`'s `access` map** (readable
   even when dev is down — stable list for clients that cache it), and enforces
   the live gateway/token **per call**: re-reads `.hamr/dev.json`, validates
   liveness, injects the bearer token on each `/__hamr/*` HTTP call. Clear
   "dev not running" errors; never crashes on missing/stale dev.
   Plus `hamr mcp install --client claude|codex|opencode` (see below).
4. ~~Action-layer parity~~ — **absorbed into the gateway dispatch table (#2).**
   `rebuild.all` reuses `RebuildAll`; `make.run` and `console.read` are
   implemented as gateway helpers (+ a filterable `dockerLogsOpts` added to
   `actions.go`). No new browser-facing HTTP routes.
5. **TUI** (`internal/devserver/tui/`):
   - Gateway state in the model, an indicator in the status/hint bar
     (on / off / "on, 0 tools") + the `M` hotkey to flip runtime state. Wire via
     the existing Runtime↔runner channels (same pattern as `HotkeyActions`).
   - **Activity on the indicator** — flash / "last: make.run 2s ago" when a tool
     fires, so agent activity is noticeable when scrolled away.
   - **Dedicated MCP tab** — a new tab (alongside the docker tabs) rendering the
     audit log live (the same lines written to `.hamr/mcp_logs.txt`).
   - **Main-log `[mcp]` attribution** — agent-triggered actions are tagged
     `[mcp]` inline in the main log (e.g. `[mcp] [docker:infra]`), distinguished
     from manual/browser actions by the bearer-token auth path.
   - Docker command output from MCP stays in the **main log** (tagged
     `[mcp] [docker:<name>]`), matching how manual docker actions log today —
     not rerouted to the docker tab.
6. **Proxy localhost default** — change the code default for `proxy.listen`
   from all-interfaces (`:3000`) to localhost (`localhost:3000`), and remove the
   explicit `proxy_listen = ":3000"` from the scaffold template
   (`templates/new/root/hamr.toml.tmpl`) so new projects inherit the localhost
   default. Document the LAN opt-in (set `proxy_listen = ":3000"` to expose).
   Existing scaffolds pin `:3000` explicitly so they're unaffected — this only
   changes the default for new/unset configs. **Independent of MCP** but bundled
   here since it's the binding half of the security model.
7. **Docs + llms.txt/llms-full.txt** (mandatory per CLAUDE.md) — config
   reference for `[dev.mcp]`, the `hamr mcp` command + `install` helper, the
   localhost-default change + LAN opt-in note, agent setup snippets per client,
   and the security model.

## Security model (summary)

- Dangerous actions are localhost-only (proxy binds localhost by default) and
  additionally token-gated for the bridge.
- The token is per-run, never leaves localhost, lives in a 0600 gitignored file.
- MCP is opt-in (`enabled=false`), default-deny on actions, and has a runtime
  kill-switch in the TUI.
- Residual risk: any local process able to read `.hamr/dev.json` can drive the
  gateway while it's on. Acceptable for a single-user dev box; documented.
- **`build` grants run of Makefile targets via `make.run`.** By default
  (`make_targets` absent) that's *every* target, including `make deploy` /
  `make release` if defined — granting `build` trusts the agent with the whole
  Makefile. Set `[dev.mcp] make_targets = [...]` to constrain it to a named
  subset. Documented loudly in the guide.

## Out of scope (v1)

- `open-browser`, `clear-log`, `quit` hotkeys — local/session-only or
  destructive to the dev session; not exposed.
- Persisting runtime toggle state back to `hamr.toml`.
- Remote/non-localhost MCP access.
