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

### Features

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

- **`validate.Phone` accepts the way people actually write phone numbers.** The
  rule matched the raw input against `^\+?[0-9]{7,15}$`, with no normalisation
  anywhere, so it only ever accepted an unbroken run of digits — `07700 900123`,
  `+44 7700 900123`, `(415) 555-1234` and `0161-496-0000` were all rejected, as
  was any value with surrounding whitespace (`Form`'s `WithTrim` is opt-in and
  trims only the ends). The value is now run through the new
  `validate.NormalizePhone`, which strips whitespace, hyphens (including Unicode
  dashes), dots, slashes and brackets, and drops a bracketed trunk zero when the
  number is in international form (`+44 (0)7700 900123` → `+447700900123`).
  Characters outside that separator set are preserved, so `(abc) 555-1234` and
  `44+7700900123` still fail. Two knock-on changes: a `+` prefix now requires a
  country code starting `1`-`9`, so the previously-accepted `+0000000` is
  rejected; and `NormalizePhone` is exported for storing numbers in a canonical
  form. Digit bounds are unchanged at 7-15, and extensions (`x123`) are still
  not accepted.

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
