# HAMR docs

HAMR is an opinionated Go full-stack framework: Echo, templ and htmx with sessions, migrations, a live-reload dev server and production defaults wired in. These guides take you from `hamr new` to a deployed app; the package reference covers every `pkg/*` API.

## Getting started

- [Installation](installation.md) — Go, the `hamr` CLI, templ and shell completion
- [Create a project](create-project.md) — `hamr new`, the wizard and its flags, first run
- [Project structure](structure.md) — The generated tree, layers, config and Makefile targets

## The basics

- [Routing and handlers](routing.md) — Server setup, route groups, page packages, errors
- [Templates and htmx](templates.md) — templ components, the layout, htmx helpers, optional Alpine.js
- [Forms and validation](validation.md) — Validators, per-field htmx validation, CSRF, flash messages
- [Static assets](static-assets.md) — CSS, JS vendoring, static pages, fingerprinting, CDN

## Database

- [Database](database.md) — PostgreSQL or SQLite, sqlx or GORM, migrations, the repository pattern

## Security

- [Authentication](authentication.md) — Password hashing, sessions, auth middleware, RBAC

## Digging deeper

- [Background jobs](background-jobs.md) — Janitor cron scheduler and async helpers
- [WebSockets](websockets.md) — Hub, subjects, rooms, applying events with htmx
- [Storage and media](storage.md) — Local and S3 storage, image and video processing

## Development

- [hamr dev](hamr-dev.md) — Dev server, watch rules, port walks, browser panel
- [Dev actions](dev-actions.md) — Widget actions, seeded test logins, staging front door

## Testing and deployment

- [Testing](testing.md) — Service, handler and E2E tests, CI
- [Deployment](deployment.md) — Docker image, CI/CD, production configuration

## Reference

- [CLI reference](cli.md) — Every `hamr` command and flag
- [hamr.toml reference](hamr-toml.md) — Full configuration schema
- [Packages](https://pkg.go.dev/github.com/FyrmForge/hamr) — API docs for every pkg/* package
