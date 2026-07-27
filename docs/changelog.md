# Changelog

A brief, human-readable summary of each release — highlights and **breaking
changes** — with a pointer to the full diff for detail. Append-only: add entries
under `## [Unreleased]`, rename to the version (e.g. `## [0.24.0]`) at release.
`hamr ai upgrade` already carries the full code diff between versions; this is
the TL;DR on top of it.

## [Unreleased]

### Breaking changes

- **CLI commands flattened.** `hamr locale gen` is now `hamr gen locale` (folded
  under the `gen` group alongside `hamr gen static`); `hamr rename module` is now
  `hamr rename-module`. Update any scripts, `hamr.toml` `cmd =` hooks, and
  Makefiles. Scaffolded projects' Makefiles already use the new names.

### Features

- **`hamr mock-serve` — headless dev mocks.** Runs the mail and Stripe mocks
  standalone (no proxy, TUI, build, or watch) for running in a dedicated
  container in a dev environment. Configured entirely via environment
  variables (no `hamr.toml`): `HAMR_MOCKS` selects which to start,
  `HAMR_MOCK_PORT` carries the app-facing surface (Stripe `/v1/*` + mail
  ingest), and an optional `HAMR_MOCK_UI_PORT` splits the human dashboards
  onto a separately-exposable listener. See [CLI reference](guide/cli.md).

### Fixes

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
