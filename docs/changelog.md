# Changelog

A brief, human-readable summary of each release — highlights and **breaking
changes** — with a pointer to the full diff for detail. Append-only: add entries
under `## [Unreleased]`, rename to the version (e.g. `## [0.24.0]`) at release.
`hamr ai upgrade` already carries the full code diff between versions; this is
the TL;DR on top of it.

## [Unreleased]

### Breaking changes

- **Frontend files consolidated under `frontend/`.** New projects put every
  frontend artifact in one directory instead of scattering four of them across
  the repo root:

  ```
  frontend/
    static/          # was static/
    dist/            # was dist/          (committed, as before)
    css/input.css    # was css/input.css  (tailwind only)
    package.json     # was ./package.json (tailwind only)
    tailwind.config.js
  ```

  Scaffold-level changes that follow from it: `[static].dir`/`dist` in
  `hamr.toml` point at `frontend/`, the Makefile runs npm inside `frontend/`,
  the Dockerfile copies `/app/frontend → /frontend` (container layout now
  mirrors local), CI's compiled-CSS check watches
  `frontend/static/css/output.css`, and `hamr vendor` writes to
  `frontend/static/js/`.

  **Existing projects are unaffected** — nothing reads a hardcoded path. `hamr
  gen static` already followed `[static]`, and `hamr sync --dir` /
  `hamr add skill`'s Alpine probe now read `[static].dir` too instead of
  assuming `static/`. To adopt the layout, move the files and update
  `hamr.toml`:

  ```toml
  [static]
  dir  = "frontend/static"   # was "static"
  dist = "frontend/dist"     # was "dist"
  ```

  Then fix the paths in `tailwind.config.js` `content` (now relative to
  `frontend/`, e.g. `../internal/web/**/*.templ`), the `[[dev.watch]]` rule
  watching `static/**/*`, and your Makefile's npm invocations. `hamr.vendor.json`
  needs no edit: re-vendoring keeps each dep's recorded `out` path, so a project
  on the old layout stays on it until you move the files and update the lock
  yourself.

- **Tailwind is a watch rule, not a daemon.** The scaffold no longer runs
  `npx tailwindcss --watch` as a `[[dev.daemon]]` — that process leaks memory
  over a long session. It's now a `[[dev.watch]]` rule running a one-shot
  `npm run css:build` on `.templ`/`input.css` changes. The `css` npm script and
  the `make css` target are gone; use `make css-build`. Cost: a cold Tailwind
  build per save (~1s in isolation, though it runs concurrently with the Go
  rebuild) instead of the warm watcher's ~40ms.

- **CLI commands flattened.** `hamr locale gen` is now `hamr gen locale` (folded
  under the `gen` group alongside `hamr gen static`); `hamr rename module` is now
  `hamr rename-module`. Update any scripts, `hamr.toml` `cmd =` hooks, and
  Makefiles. Scaffolded projects' Makefiles already use the new names.

- **Stripe mock moved to stripe-go v86 and API version `2026-08-26.dahlia`.**
  It was on v82 and `2025-08-27.basil`. New projects import
  `github.com/stripe/stripe-go/v86`. Existing projects with the mock enabled
  must move too. stripe-go's `webhook.ConstructEvent` rejects events from a
  different release train, so a v82 (`basil`) app rejects every mock event.
  Migrate: swap `stripe-go/v82` for `stripe-go/v86` in imports, then
  `go get github.com/stripe/stripe-go/v86@v86.4.2 && go mod tidy`. The
  scaffold's calls compile unchanged. Apps on v85 (also `dahlia`) keep working.

- **`[dev.stripe] enabled` is replaced by `mode`.** A config that still sets
  `enabled` fails to load. Migrate: `enabled = true` → `mode = "mock"`;
  `enabled = false` → remove it (or `mode = "off"`). `hamr dev` now also
  injects `STRIPE_MOCK` and the `STRIPE_WEBHOOK_SECRET*` vars into your app,
  overriding `.env`, so `webhook_secret` in `hamr.toml` is the source of truth
  in mock mode.

### Fixes

- **Builds no longer show as running after they finish.** A rebuild that
  produced a burst of log lines could discard the event that clears the build,
  leaving the `hamr dev` status bar and the browser panel's dot spinning on
  work that had already finished. The browser case persisted until the next
  page reload, so a rule with `reload = "none"` stayed stuck for the session.
  Rules that only run a process, and builds cancelled while waiting on a
  dependency, could leave the same stuck entry.

- **hamr reads common `.env` lines the way your app does.** `hamr dev` and
  `hamr sync` had three separate `.env` parsers that disagreed with godotenv and
  each other. A
  trailing comment (`STRIPE_KEY=sk_test_abc # sandbox`) stayed in the value,
  so `stripe listen` got a broken key. An `export DATABASE_URL=...` line kept
  `export` in the key, so the port-walk rewrite went out under the wrong name.
  One shared parser now strips `export`, surrounding quotes, and a ` #`
  comment after an unquoted value. Escape sequences inside quotes are still
  not unescaped, but an escaped quote no longer ends the value.

- **The Stripe mock's money paths agree with each other.** Refunds are
  capped at the captured amount, not the authorised one, so a partial capture
  cannot be over-refunded. A disputed charge refuses refunds
  (`charge_disputed`), a fully refunded charge refuses disputes, and a partial
  refund shrinks the disputable amount. Transfers need funding: with
  `source_transaction` the charge must have enough left after refunds and
  earlier transfers from it, without one the platform balance must cover the
  amount (`balance_insufficient` either way). Disputes on direct charges land
  on the connected account's ledger instead of the platform's, and
  `refund_application_fee` moves the pro-rata fee back to the connected
  account.

- **The `make <target>` spinner clears even when the event bus drops the done
  event.** A chatty target could flood the per-subscriber channel, lose
  `EvMakeDone`, and leave the status bar spinning until the next restart. The
  TUI now also clears it when the run's own result arrives.

- **`touch` on a watched file reruns its `[[dev.watch]]` rule.** `touch`
  changes only the timestamp, which the watcher dropped as a non-modification
  event, so `touch .env` never restarted the app even though `.env` sits in the
  site rule's `watch`. A timestamp bump now counts; debounce still folds a
  burst into one run. Tools that rewrite timestamps without editing (some
  editors, `git checkout`) now also trigger a rebuild. A `chmod` or `chown`
  that leaves the timestamp alone is still ignored, so a rule that
  `chmod +x`es files in its own watch set does not loop.

- **The scaffold integration tests pick free ports instead of hardcoded ones.**
  They shell `docker compose up` directly rather than going through `hamr dev`,
  so hamr's own compose port walk never applied to them, and the scaffold
  hardcodes 5432 for postgres and 9000/9001 for the S3 mock. Any developer
  already running another project's containers on those ports could not run
  `make ai` at all — and the failure was not always a clean bind error: in one
  case compose came up, the scaffolded app connected to the OTHER project's
  postgres, and it failed much later with "database does not exist". The
  harness now rewrites each published host port in the generated compose file
  to a free one and repoints `.env` to match. The app's own port is picked the
  same way instead of the hardcoded 18080-18082.

- **`hamr setup`'s form test no longer fails at random.** It drove the whole
  picker from a scripted keystroke string, but huh advances between form groups
  with `tea.Sequence`, which bubbletea resolves asynchronously while the input
  reader keeps feeding keys — so a keystroke aimed at a later group could land
  on the finished one and be swallowed. It failed roughly half the time under
  repetition and occasionally took the whole package with it. The headless run
  now drives only the first group, the access selects are exercised
  field-by-field with no group transitions in play, and the form runs under a
  timeout so a dropped key fails the test instead of hanging it.

- **Scaffolded inline field re-validation now works, without widening the CSP.**
  The auth forms' `input[this.closest(...).querySelector(...)]` trigger filter
  never fired as intended: htmx compiles `hx-trigger` `[...]` filters with
  `Function()`, the scaffolded CSP has no `'unsafe-eval'`, so htmx dropped the
  filter and every keystroke triggered a validation request. The condition now
  lives in `static/js/main.js`, which debounces 300ms and fires a
  `hamr:revalidate` event only while the field is already showing an error; the
  inputs use `hx-trigger="blur, hamr:revalidate"`. Inputs pair with their error
  span by id convention (`name="x"` <-> `id="error-x"`); `data-hamr-watch="y"`
  gates on another field's error instead, for cross-field validation. The CSP is
  unchanged — no `'unsafe-eval'`. Same change in `AGENTS.md` and the forms guide.

### Features

- **hamr mock image.** Each release now publishes
  `ghcr.io/fyrmforge/hamr:<version>` and `:latest` for linux/amd64 and arm64.
  It runs `hamr mock-serve` by default, so a compose stack can use
  `image: ghcr.io/fyrmforge/hamr:v0.38.0` instead of building hamr from source.
  The Stripe mock's dispute fee is now 2000 (was 1500) and, as in real
  Stripe, a won dispute no longer returns it. The mock-serve guide
  used a `mock serve` command that doesn't exist; it now shows the image.

- **Stripe listen mode for `hamr dev`.** Press `S` to swap the Stripe mock for
  `stripe listen` against your real sandbox, and back. hamr runs the Stripe CLI
  with `STRIPE_KEY` from `.env`, forwards to your webhook URLs (thin events and
  connected-account events included), injects the signing secret it prints and
  restarts your app — no more copying `whsec_` into `.env`. Start there with
  `[dev.stripe] mode = "listen"`. Live keys are refused; a listener that fails
  or exits falls back to the mock; editing `STRIPE_KEY` restarts it. New
  `connect_webhook_url` / `thin_connect_webhook_url` (the mock uses them too)
  and `thin_events` options, a `stripe.mode` MCP tool, and `dev.info` reports
  the mode. See `[dev.stripe]` in the hamr.toml guide.

- **Stripe mock covers Connect on Accounts v2.** Stripe no longer lets new
  platforms create v1 Express accounts, so the mock now serves
  `/v2/core/accounts` and `/v2/core/account_links`. Onboarding sets the
  requested capabilities active and sends v2 thin events to the new optional
  `[dev.stripe] thin_webhook_url`, signed with the same secret. It also adds
  transfers and reversals, `GET /v1/refunds`, checkout session expire, balance
  transactions with a fee on every charge, balance settings, disputes and
  connected-account payouts driven from the dashboard, and replay of repeated
  idempotency keys. The `/__hamr/stripe` page gains v2 accounts, disputes and an
  event log where any event can be resent with the same id. Two read-only pages
  show the records laid out like Stripe's dashboards: a platform dashboard at
  `/__hamr/stripe/dashboard` (payments, balances, connected accounts,
  transfers, payouts, disputes, events) and an Express view per connected
  account at `/__hamr/stripe/express/<account>`. New projects
  scaffold a second webhook route at `/api/webhooks/stripe/v2` with
  `STRIPE_WEBHOOK_SECRET_V2`, and a `stripe.Client`. Existing apps see a few
  extra fields and events: charges carry `balance_transaction`, refunds with
  `reverse_transfer` also fire `transfer.reversed`, and connected-account
  payout events set `account`. See [Stripe mock](guide/pkg/stripemock.md).

- **Cloudflare as a trusted proxy.** `TRUSTED_PROXIES=cloudflare` (or
  `server.WithTrustedProxies("cloudflare")`) trusts Cloudflare's edge ranges so
  `RealIP()` returns the visitor, not a Cloudflare IP. The list ships pinned as
  `server.CloudflareCIDRs` and `Start()` refreshes it from Cloudflare every 24h;
  a bad fetch keeps the current list. Mix with your own proxy's CIDR when
  stacking. See [server guide](guide/pkg/server.md#cloudflare).

- **Public tunnel for `hamr dev`.** Press `T` to put the dev proxy on the
  internet through your locally installed `cloudflared` (default, no account)
  or `ngrok`, or any command via `[dev.tunnel] cmd`. hamr sets `BASE_URL` (or
  the vars in `env`) to the public URL and restarts your app; `T` again turns it
  off. The tunnel gets its own proxy listener that allows only live reload, the
  logo and the mail/SMS/Stripe mocks under `/__hamr/`; every other hamr route,
  including any added later, returns 403. Process output stays out of live
  reload and error pages. See `[dev.tunnel]` in the hamr.toml guide.

- **One event bus for the dev server, and a system indicator in the TUI status
  bar.** The status bar now says what `hamr dev` is doing right now —
  `⠙ building <rule>`, `⠙ make <target>`, `⠙ restarting` (all spinning while
  the work is in flight), `ERR: <rules>`, or `● OK` — for work triggered from anywhere: a file save, a hotkey, the browser
  dev panel, or an MCP agent.

  Underneath, the TUI stopped being wired a callback at a time and became a
  subscriber to the same event stream the browser dev panel reads, and the `m`
  hotkey stopped spawning its own `make`. It now goes through
  `DevActions.RunMake` like every other command, which **fixes make output being
  invisible to agents**: `logs.read` with `rule: "make:<target>"` returned
  nothing for TUI-launched runs, despite the `make.run` tool description
  promising otherwise. Output from any make run now reaches the hamr tab, the
  browser log overlay and `logs.read` together, ending with a
  `[make:<target>] exited <n>` marker.

  The indicator is a stock ticker, not a single slot: everything in flight
  scrolls past, joined by `•`, each item spinning. That includes two phases
  that used to report nothing at all — the **initial build** on startup and
  **docker compose coming up** (`⠙ starting postgres`), which between them are
  most of what a cold start spends its time on. A single item that fits sits
  still rather than scrolling for no reason.

  `ERR: <rules>` now joins the ticker and reddens it instead of outranking
  everything, so a rebuild you triggered to fix a failing rule is visible while
  it runs rather than hidden behind the error it's fixing.

  With the status bar reporting the run, the modal that used to sit over the TUI
  while a target ran is gone: `↩` closes the palette and hands the keyboard
  straight back, so you can scroll logs, switch tabs and search while `make`
  works. The `Done ✓` / `Failed ✗` box went with it — the
  `[make:<target>] exited <n>` line is the result.

  The rule going forward is in `docs/adr/004-dev-event-bus.md`: dev-server state
  goes out as a typed broker event, consumer actions come in through
  `DevActions`, and neither gets a bespoke hook. Two visible trades: `[make:...]`
  prefixes no longer keep a stable per-target colour, they use the same rotation
  as every other rule; and there is no longer a key that cancels a running make
  — `Ctrl+C` quits the TUI, which kills the run with it.

- **Restart the dev server without leaving the TUI.** `R` in `hamr dev` tears
  the runner down and re-runs its whole startup lifecycle in place: config
  re-read, docker compose brought up, ports re-resolved, `.env` re-injected,
  builds and daemons restarted, watcher rebuilt. The TUI and its log buffers
  survive. This is the escape hatch for state the runner only reads at startup
  — a port that is now clashing, an edited `.env`, a container that came up
  wrong — which previously meant quitting and starting over. Same path the
  runner already took on a `hamr.toml` change, now triggerable on demand.

  `R` also works while `hamr dev` is parked on a `hamr.toml` parse error, where
  it retries immediately. Previously only a write to `hamr.toml` got you out of
  that state, which is useless when the config is valid and startup failed on a
  clashing port or a bad `.env` — neither of which touches that file.

  The matching MCP tool is **`dev.restart`**, added to the `dev` area at
  `write` (the area was read-only before, so grant `dev = "write"` to expose
  it). It lets an agent recover a wedged dev server itself instead of asking.
  It returns as soon as the restart is queued, because the proxy carrying the
  call is one of the things torn down; the bridge re-reads `.hamr/dev.json` per
  call and finds the new port and token by itself, so an agent just waits a few
  seconds and polls `dev.info`. The reply is written and flushed before the
  restart is requested, so a successful restart never surfaces to the agent as a
  connection error. Both `R` and `dev.restart` are refused while the dev server
  has been up for less than 5 seconds: an agent that has decided the server is
  wedged would otherwise spend a full teardown-and-rebuild per call, and a
  double-press of `R` would cost two restarts.

- **`make e2e-local` spawns its own server.** The scaffolded local e2e mode
  builds `bin/site`, starts it on the first free port from 8080 (walking +1
  past busy ports, like `hamr dev`), waits for `/api/health`, seeds, runs,
  and kills it. Previously it assumed something was already listening on
  8080 and silently tested whatever was there. `E2E_SERVER_URL` still
  attaches to an existing server. Also fixed: the e2e Makefile targets were
  not `.PHONY`, so `make e2e` reported "up to date" because the `e2e/`
  directory exists, and `TestMain` deferred teardown past `os.Exit` so
  nothing was ever cleaned up.

- **`pkg/e2e` browser launch knobs.** `SetupBrowser` gains `WithGPU`
  (`E2E_GPU`, default off — `--disable-gpu` was previously hardcoded),
  `WithBrowserPath` (`E2E_BROWSER_PATH`, use a system Chrome instead of rod's
  download) and `WithWindowSize` (`E2E_WINDOW_SIZE=1280x800`). Headed local
  debugging: `E2E_HEADLESS=false E2E_GPU=true E2E_WINDOW_SIZE=1280x800`.

- **Licensed under Apache 2.0.** `LICENSE` and `NOTICE` added at the repo root;
  copyright FyrmForge Limited. The README already pointed at `LICENSE`.

- **`hamr add skill` — interactive picker and two new skills.** Run with no
  arguments for a huh picker (agents, then skills); `hamr add skill claude`
  stays scriptable with `--skills hamr,qa-loop,pr-publish` (default all). New
  alongside the framework skill: `qa-loop`, an iterative QA test-and-fix loop
  over Playwright + hamr MCP with device profiles, watch-lists and a per-round
  scorecard, installed as `.claude/skills/hamr-qa-loop/`; and `pr-publish`, a
  GitHub PR workflow with a structured body, gist-hosted screenshots and a
  stepped flow GIF, installed as `.claude/skills/hamr-pr-publish/`. The stale
  `qa.md`/`qa-loop.md` files that previously sat undiscoverable inside the
  hamr skill directory are gone (superseded by the `qa-loop` skill).
  `hamr setup`'s skills screen gained the same skill multiselect. New targets
  `codex` and `opencode` install the identical skills to the cross-tool
  standard `.agents/skills/` (shared — picking both writes once; opencode
  also reads `.claude/skills/` natively).

- **`hamr dev --headless` — no TUI, one plain stream on stdout.** For an AI
  agent or CI running the dev server in the background
  (`hamr dev --headless > dev.log &`): hamr's own lines, rule/daemon output
  and `docker compose logs` all go to stdout; no hotkeys, stop with SIGTERM.
  Switches on automatically whenever stdout is not a terminal. `hamr mcp` and
  the `/__hamr/*` endpoints work as before.

- **Dark comfort filter for the proxied site.** A "Dark filter" checkbox in
  the browser dev panel inverts the page (`invert(1) hue-rotate(180deg)`,
  media and hamr's own overlay re-inverted) so a light-mode app is bearable
  to work on. `[dev].dark_filter = true` (or in `.pref.hamr.toml`) sets the
  initial state; the toggle lives in the `hamr dev` process, is kept in sync
  across open tabs over SSE, and is never written back to `hamr.toml`.

- **`hamr add service` — add a new Go binary to an existing project.** A
  stepped wizard (or flags) scaffolds one of four service types — `worker`
  (background loop with graceful shutdown), `api` (JSON server on its own
  port), `html` (templ server reusing the project layout), or `empty` (bare
  `Run()` stub) — as `cmd/<name>/` (main.go + Dockerfile) plus
  `internal/<name>/`, appends a `[[dev.watch]]` rule to `hamr.toml` so `hamr
  dev` builds and runs it alongside the site, and appends `<NAME>_PORT` to
  `.env`/`.env.example` for the HTTP types. `--db`, `--auth`, and `--locale`
  optionally wire the project's repo store, session validation against the
  shared store, and the locale bundle, matching the choices recorded in
  `hamr.toml [options]`.

- **`templint:ignore` — inline lint suppression for `.templ` files.** A
  deliberate exception no longer forces you to switch a rule `"off"` project-wide:

  ```
  // templint:ignore no-native-form-actions -- GitHub's manifest flow requires a browser form POST
  <form method="post" action={ templ.SafeURL(action) }>
  ```

  The directive lives in a `//` comment and covers the next line, or its own
  line when it trails source content. The rule list is comma-separated and
  optional (bare `// templint:ignore` covers everything); anything after ` -- `
  is a human reason. Because rules anchor their diagnostic to the line a tag
  *opens* on, the directive goes above the `<form`, not above the offending
  attribute inside a multi-line tag. Two non-configurable diagnostics keep
  directives honest: `unknown-rule` (error) for a mistyped rule ID, so a typo
  cannot silently suppress nothing, and `unused-suppression` (warning) for a
  directive that suppressed nothing, so stale ones get cleaned up. A directive
  naming a rule that is switched `"off"` reports neither. See
  [Templint](guide/pkg/templint.md#inline-suppression).

- **`hamr setup` — interactive AI-agent setup.** One pass over the decisions
  that were previously a mix of CLI flags and hand-edited TOML: which agents
  (claude/codex/opencode) get the hamr MCP bridge registered, whether the
  gateway is enabled, `deny`/`read`/`write` per `[dev.mcp.access]` area, and
  which agents get the hamr skill installed. Seeded from the project's current
  state, writes only `[dev.mcp]` / `[dev.mcp.access]` in `hamr.toml` (comments
  and every other table untouched) plus the per-agent config `hamr mcp install`
  already wrote. It also upserts a `## hamr MCP` section into `AGENTS.md` and
  `CLAUDE.md` scoped to the granted areas, so agents stop defaulting to the
  manual equivalent (tailing logs, running `make build`, asking what an email
  said); disabling the gateway removes the section again. `--dry-run` previews.
  See [CLI reference](guide/cli.md#hamr-setup).

- **`hamr compose` — docker compose with the config `hamr dev` is actually
  using.** When a compose host port is busy, `hamr dev` walks it and records the
  result in `.hamr/compose.<name>.override.yaml`. Anything else calling
  `docker compose -f docker/docker-compose.yaml` merged only the base file, so
  compose saw the running stack as drifted and recreated it on the original
  ports — a hard failure (`Bind for 0.0.0.0:9000 failed: port is already
  allocated`) precisely when the walk was needed. `hamr compose up -d` /
  `down -v` / `exec …` merge what hamr merges; `--name` picks the entry when
  there is more than one; streams and exit code pass through. The scaffold's
  `make docker-up` / `docker-down` / `docker-delete` targets now use it. This is
  the compose-side counterpart to `hamr env`.
  See [CLI reference](guide/cli.md#hamr-compose).

- **`dir` on watch rules and daemons.** `[[dev.watch]]` and `[[dev.daemon]]`
  accept `dir = "subdir"`, the working directory for `cmd`/`run`, relative to
  the project root (default: the project root). `watch`/`ignore` globs stay
  root-relative, so a rule can watch the whole repo while building inside a
  subdirectory. A `dir` that doesn't exist, isn't a directory, is absolute, or
  resolves outside the project root (symlinks included) fails at config load
  rather than silently running in the wrong place.

- **`hamr mock-serve` — headless dev mocks.** Runs the mail and Stripe mocks
  standalone (no proxy, TUI, build, or watch) for running in a dedicated
  container in a dev environment. Configured entirely via environment
  variables (no `hamr.toml`): `HAMR_MOCKS` selects which to start,
  `HAMR_MOCK_PORT` carries the app-facing surface (Stripe `/v1/*` + mail
  ingest), and an optional `HAMR_MOCK_UI_PORT` splits the human dashboards
  onto a separately-exposable listener. See [CLI reference](guide/cli.md).

### Fixes

- **Dev mode no longer caches static assets.** `server.New` mounted
  `middleware.CacheControl(false)` unconditionally, so `.css`/`.js` under
  `/static` were served with `public, max-age=86400` even with
  `WithDevMode(true)`. Dev URLs aren't fingerprinted, so after any rebuild the
  browser kept serving the stale stylesheet for up to a day — pages looked
  broken until a hard refresh. The middleware now receives `s.devMode`: dev
  sends `no-cache, no-store, must-revalidate` on every response; production
  behavior is unchanged. The two static-cache tests asserted the prod headers
  under `WithDevMode(true)` — which is exactly how this slipped through — and
  now run in prod mode, with a new dev-mode test pinning the no-cache header.

- **Scaffolded projects no longer 500 on their first 404.** `ctx.Get` (and
  `GetAs`) dereferenced the `echo.Context` without a nil check, but components
  legitimately render without a request: `middleware.ErrorPages` builds its page
  from a `func(code int, message string)`, so the scaffold's error page calls
  `@Layout(nil, ...)`. Any accessor the layout used — `GetFlash`, `GetSubject`,
  `GetSubjectID` — then panicked, and Echo's `Recover` turned the intended
  styled 404 into a 500 plus a stack trace in the log, on every miss including
  the `/favicon.ico` browsers fetch unprompted. The accessors now return zero
  values on a nil context; `MustGet`/`MustGetAs` still panic, but say
  `ctx: nil echo.Context for key <name>` instead of dereferencing nil.

- **Postgres healthcheck no longer reports ready mid-initdb.** The scaffold's
  compose healthcheck ran `pg_isready -U postgres`, which probes the unix
  socket. On a fresh volume the postgres entrypoint runs `initdb` against a
  temporary server that listens on the socket only, so the check passed while
  the real server did not yet exist: `wait_ready = true` could be satisfied
  early, and anything that then connected raced the shutdown with
  `FATAL: the database system is shutting down`. Now probes TCP
  (`pg_isready -U postgres -h 127.0.0.1`), which the bootstrap server does not
  listen on. Existing projects: apply the same one-word change to
  `docker/docker-compose.yaml`.

- **Walked compose ports are now actually published.** When `hamr dev` walked a
  docker-compose host port off a collision (e.g. a second project's postgres
  taking 5432), the generated `.hamr/compose.<name>.override.yaml` tagged the
  `ports:` list with `!override` — but previously used `!reset`, which Compose
  treats as "delete this key", discarding the rewritten bindings. The services
  came up with no published ports at all and the app hit connection-refused on
  the walked port it had been handed via `.env`. Only triggered when a walk
  actually happened. The override is regenerated on every `hamr dev` start, so
  the fix applies with no manual cleanup.

- **S3 `Save` accepts non-seekable readers.** Piping an `Open` result (or any
  non-seekable stream) straight into `S3Storage.Save` previously failed with
  "request stream is not seekable" because the AWS signer needs a seekable body
  to hash. `Save` now buffers non-seekable bodies before upload; seekable
  readers (files, `bytes.Reader`, `multipart.File`) still stream directly. The
  buffer is bounded by the new `WithMaxUploadBuffer` option (default 64 MiB) so
  a large non-seekable body fails rather than allocating unbounded memory.

## [0.24.0] - 2026-06-17

Whole-repo code-review remediation across the libraries, dev server, Stripe/mail
mocks, TUI, and CLI. Full diff:
[`v0.23.2...v0.24.0`](https://github.com/FyrmForge/hamr/compare/v0.23.2...v0.24.0).

### ⚠️ Breaking

- **CORS denies by default** when `AllowOrigins` is empty (was Echo's `*`) —
  pass explicit origins if you mount it.
- **`validate.URL` accepts only `http`/`https`** — other schemes now fail.
- **Client IP behind a proxy**: set `WithTrustedProxies` / `TRUSTED_PROXIES` to
  your proxy's CIDR, or `RealIP()` (rate-limit key + audit IP) returns the proxy
  IP. Never `0.0.0.0/0`. See `docs/guide/pkg/server.md`.

### Highlights

- **Security**: trusted-proxy IP extraction (XFF no longer blindly trusted),
  audit-log redaction, CSRF cookie flags, argon2 version check, media size
  bounds + category-scoped serving (S3 backend faults now return `500`, not a
  misleading `404`).
- **Dev server**: fixed quit/shutdown hangs, process-reaping and
  scheduler/port-walk/watcher races, proxy response truncation.
- **Stripe/mail mocks**: concurrency-safe (clone-on-read) and higher fidelity
  (checkout PI+Charge, manual capture, accurate decline/resend).
- **TUI & CLI**: rendering fixes (search/selection/bars/modals/unicode);
  `rename`, `upgrade`, and `localegen` hardening.
