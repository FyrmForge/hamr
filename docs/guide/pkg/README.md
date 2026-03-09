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
10. [Storage](storage.md) — Pluggable file storage (local, S3)
11. [Media](media.md) — Image and video processing

## Middleware & Auth

12. [Middleware](middleware.md) — Auth, RBAC, flash, rate limiting, CSRF, CORS
13. [Auth](auth.md) — Password hashing and session management

## Infrastructure

14. [Janitor](janitor.md) — Cron-based background task scheduler
15. [Async](async.md) — Concurrency helpers
16. [Sync](sync.md) — File synchronization (local to S3)
17. [WebSocket](websocket.md) — Session and room-based real-time hub

## Testing

18. [E2E](e2e.md) — Reusable Go-Rod browser helpers for end-to-end tests

## Tooling

19. [Templint](templint.md) — Static linter for `.templ` files
20. [Static Generation](static-generation.md) — Build-time page generation
21. [Dev](dev.md) — Live reload development environment
