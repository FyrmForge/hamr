# HAMR Guide

Learn how to build full-stack Go applications with HAMR.

## CLI Reference

[CLI Reference](cli.md) — All `hamr` commands and flags

| Command | Description |
|---------|-------------|
| [`hamr new`](cli.md#hamr-new) | Scaffold a new project |
| [`hamr dev`](cli.md#hamr-dev) | Dev server with live reload |
| [`hamr sync`](cli.md#hamr-sync) | Sync directory to S3 |
| [`hamr lint templ`](cli.md#hamr-lint-templ) | Lint `.templ` files |
| [`hamr vendor`](cli.md#hamr-vendor) | Vendor frontend JS deps |
| [`hamr rename module`](cli.md#hamr-rename-module) | Rename Go module + imports |
| [`hamr version`](cli.md#hamr-version) | Print version |

## Guides

1. [Project Setup](01-project-setup.md) — Scaffolding, project structure, config
2. [Development Workflow](02-dev-workflow.md) — Dev server, watch rules, live reload
3. [Database](03-database.md) — PostgreSQL connection, migrations, repo pattern
4. [Handlers & Routing](04-handlers-routing.md) — Server, routes, middleware, error handling
5. [Templates & Frontend](05-templates-frontend.md) — Templ, HTMX, Alpine.js
6. [Forms & Validation](06-forms-validation.md) — Validators, CSRF, flash messages
7. [Authentication](07-authentication.md) — Sessions, auth middleware, login/register
8. [File Storage](08-file-storage.md) — Local/S3 storage, media processing
9. [Static Assets](09-static-assets.md) — CSS, JS vendoring, S3 sync, CDN
10. [Background Jobs](10-background-jobs.md) — Janitor scheduler, async helpers
11. [Real-Time](11-real-time.md) — WebSocket hub, rooms, HTMX integration
12. [Testing](12-testing.md) — Unit tests, E2E with go-rod, CI setup
13. [Deployment](13-deployment.md) — Docker, CI/CD, production config

## Package Reference

[Package Reference](pkg/README.md) — Full API docs for every `hamr/pkg` package
