# HAMR security findings

30 findings from a multi-pass audit (Claude + codex, cross-verified against source). One file per finding, sorted by CVSS v3.1 base score descending.

Filename format: `NN-CVSS-slug.md`.

CVSS scores the *technical* exploitability of the default install — not business impact. Use it as a triage order, not a final decision.

## High (7.0–8.9)

- [01 — 7.5 — Stripe webhook secret defaults to empty](01-7.5-stripe-webhook-empty-secret.md)
- [02 — 7.5 — Rate limiter trusts X-Forwarded-For; memory store unbounded](02-7.5-ratelimit-spoof-and-unbounded.md)
- [03 — 7.5 — Scaffold `.env` ships insecure defaults](03-7.5-scaffold-env-defaults.md)
- [04 — 7.5 — Media uploads buffered fully into memory](04-7.5-media-memory-amplification.md)

## Medium (4.0–6.9)

- [05 — 6.6 — LocalStorage symlink traversal](05-6.6-localstorage-symlink-traversal.md)
- [06 — 6.1 — Session tokens stored plaintext in DB](06-6.1-plaintext-session-tokens.md)
- [07 — 6.1 — Media DetectType falls back to client Content-Type](07-6.1-media-content-type-fallback.md)
- [08 — 5.5 — Symlink escape in static file serving](08-5.5-static-symlink-escape.md)
- [09 — 5.4 — `users.active` not checked in Authenticate](09-5.4-inactive-user-login.md)
- [10 — 5.3 — `/ws` mounted with no auth](10-5.3-websocket-no-auth.md)
- [11 — 5.3 — No HTTP socket timeouts (slowloris)](11-5.3-http-server-timeouts.md)
- [12 — 5.3 — No rate-limit on scaffold login/register](12-5.3-scaffold-auth-no-ratelimit.md)
- [13 — 5.3 — User-enumeration timing in Authenticate](13-5.3-user-enum-timing.md)
- [14 — 5.3 — ErrorPages reflects HTTPError.Message into 5xx page](14-5.3-errorpages-message-disclosure.md)
- [15 — 4.8 — No HSTS support in secure middleware](15-4.8-no-hsts.md)
- [16 — 4.8 — Global gzip → BREACH on authenticated HTML](16-4.8-breach-on-authenticated-gzip.md)
- [17 — 4.7 — Audit middleware logs raw query strings + path params](17-4.7-audit-log-leaks-secrets.md)
- [18 — 4.7 — SQLite dir 0755, no enforced 0600 on .db/WAL](18-4.7-sqlite-file-permissions.md)
- [19 — 4.3 — Stripe webhook handler lacks idempotency](19-4.3-stripe-no-idempotency.md)
- [20 — 4.3 — CacheControl can mark dynamic auth content as public](20-4.3-cachecontrol-public-auth.md)

## Low (0.1–3.9)

- [21 — 4.0 — RenameModule follows symlinks during write-back](21-4.0-rename-module-symlink.md)
- [22 — 4.0 — VendorCustom `out` path traversal](22-4.0-vendor-custom-traversal.md)
- [23 — 3.7 — Argon2 PHC version field not enforced](23-3.7-argon2-version-unchecked.md)
- [24 — 3.5 — CSRF wrapper leaves SameSite/Path on Echo defaults](24-3.5-csrf-cookie-weak-defaults.md)
- [25 — 3.1 — Open-redirect-capable redirect helpers](25-3.1-open-redirect-helpers.md)
- [26 — 2.6 — TrustedSubject blindly trusts X-Subject-ID](26-2.6-trusted-subject-header-trust.md)
- [27 — 2.6 — CSP allows 'unsafe-inline'](27-2.6-csp-unsafe-inline.md)
- [28 — 2.6 — Image serve sets Content-Type from extension](28-2.6-image-content-type-from-ext.md)
- [29 — 2.6 — Unsigned flash cookie](29-2.6-unsigned-flash-cookie.md)
- [30 — 2.1 — Scaffold Dockerfile runs as root](30-2.1-dockerfile-runs-as-root.md)

## Audit method

1. Claude pass — manual read of `pkg/*` and `internal/cli/generator/templates/new/*`
2. Codex pass A — independent review, no priming
3. Codex pass B — consolidate both lists, dedupe, verify against source (one false positive killed: CSRF cookie expiry — Echo's defaults set MaxAge=86400)
4. Codex pass C — gap-hunt against the consolidated list (9 new findings)
5. Codex pass D — adversarial CVSS scoring against my drafts

Findings 21–30 are framework-debt / footguns more than active vulnerabilities. Triage threshold is yours to set.
