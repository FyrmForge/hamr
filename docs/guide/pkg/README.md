# Package Reference

API reference for every `hamr/pkg` package.

## Foundation

1. [Config](config.md) — Environment-based configuration helpers
2. [Server](server.md) — Echo wrapper with functional options and graceful shutdown
3. [Logging](logging.md) — Context-aware structured logging

## Core

4. [Ctx](ctx.md) — Type-safe Echo context keys
5. [Respond](respond.md) — HTTP response helpers (HTML, JSON, errors)
6. [Validate](validate.md) — Pure-function validators
7. [HTMX](htmx.md) — Request detection and response headers
8. [Ptr](ptr.md) — Generic helpers for creating and dereferencing pointers

## Data & Storage

9. [DB](db.md) — PostgreSQL connection, retry, and migrations
10. [DB SQLite](sqlite.md) — SQLite connection and migrations (pure-Go, no CGO)
11. [Storage](storage.md) — Pluggable file storage (local, S3)
12. [Media](media.md) — Image and video processing

## Middleware & Auth

13. [Middleware](middleware.md) — Auth, RBAC, flash, rate limiting, locale, CSRF, CORS
14. [Auth](auth.md) — Password hashing and session management
15. [I18n](i18n.md) — Internationalisation: translation bundles, plurals, RTL

## Infrastructure

16. [Janitor](janitor.md) — Cron-based background task scheduler
17. [Async](async.md) — Concurrency helpers
18. [Sync](sync.md) — File synchronization (local to S3)
19. [WebSocket](websocket.md) — Session and room-based real-time hub

## Email

20. [Email](email.md) — Provider-agnostic `Sender` interface for outbound mail

## Third-party Mocks

21. [Stripemock](stripemock.md) — Local Stripe-compatible HTTP backend that real `stripe-go` talks to in dev
22. [Emailmock](emailmock.md) — Local mock `email.Sender` with inbox viewer at `/__hamr/mail`

## Testing

23. [E2E](e2e.md) — Reusable Go-Rod browser helpers for end-to-end tests

## Tooling

24. [Templint](templint.md) — Static linter for `.templ` files
25. [Static Generation](static-generation.md) — Build-time page generation
26. [Dev](dev.md) — Live reload development environment
