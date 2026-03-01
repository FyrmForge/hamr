# HAMR

<img src="hamr_logo.png" alt="HAMR" width="200" align="left">

An opinionated Go full-stack framework and project scaffolding CLI. HAMR extracts proven patterns from production Go web applications into reusable packages and gives you a working project with one command.

<br clear="left">

```
hamr new myproject
```

You get a production-ready Go + Templ + HTMX + Alpine.js application with sensible defaults, structured logging, database migrations, session auth, and AI-ready documentation baked in.

## What's in the box

HAMR is two things:

1. **Framework library** (`pkg/`) -- reusable Go packages your project imports
2. **CLI tool** (`cmd/hamr/`) -- scaffolds new projects that use the framework

### Framework packages

| Package            | What it does                                                                       |
| ------------------ | ---------------------------------------------------------------------------------- |
| `pkg/config`       | Env-based configuration with typed accessors and `.env` file loading               |
| `pkg/logging`      | Context-aware structured logging via `slog` (JSON in prod, coloured in dev)        |
| `pkg/ptr`          | Generic and concrete pointer helpers                                               |
| `pkg/validate`     | Pure-function validators with custom messages and a plugin registry                |
| `pkg/auth`         | Argon2id password hashing, token generation, session management                    |
| `pkg/db`           | Database connection with retry, keep-alive, and migration runner                   |
| `pkg/htmx`         | HTMX request detection and response header helpers                                 |
| `pkg/respond`      | Content-negotiated responses (HTML via Templ or JSON from the same handler)        |
| `pkg/ctx`          | Type-safe Echo context keys using generics                                         |
| `pkg/middleware`   | Auth, RBAC, flash messages, rate limiting, request ID, caching, audit, CSRF, CORS  |
| `pkg/server`       | Echo wrapper with functional options and lifecycle hooks                           |
| `pkg/janitor`      | Background task scheduler                                                          |
| `pkg/storage`      | File storage interface with local filesystem and S3/R2/MinIO backends              |
| `pkg/notify`       | Notification sender interface with async dispatch                                  |
| `pkg/websocket`    | Session and room-based WebSocket hub with HTMX integration                         |
| `pkg/client`       | Inter-service HTTP client with header propagation                                  |

### CLI commands

| Command                     | What it does                                    |
| --------------------------- | ----------------------------------------------  |
| `hamr new <name>`           | Scaffold a new project with interactive options |
| `hamr add service <name>`   | Add a service to an existing project            |
| `hamr version`              | Print version and commit                        |

## Install

### CLI tool

```bash
go install github.com/FyrmForge/hamr/cmd/hamr@latest
```

### Framework packages

Import the packages you need in your `go.mod`:

```bash
go get github.com/FyrmForge/hamr@latest
```

Then import individual packages:

```go
import (
    "github.com/FyrmForge/hamr/pkg/config"
    "github.com/FyrmForge/hamr/pkg/logging"
    "github.com/FyrmForge/hamr/pkg/server"
)
```

## Quick start

```bash
# Install the CLI
go install github.com/FyrmForge/hamr/cmd/hamr@latest

# Create a new project
hamr new myproject

# Follow the prompts to choose:
#   - Go module path
#   - CSS approach (plain CSS with design system or Tailwind)
#   - Auth scaffolding
#   - File storage, WebSocket, and notification support

# Run it
cd myproject
docker compose up -d        # start Postgres
make migrate                # run migrations
make dev                    # start dev server
```

## Generated project structure

```
myproject/
├── cmd/server/
│   ├── main.go              # Bootstrap: config, db, migrate, services, server
│   └── Dockerfile
├── internal/
│   ├── config/              # App-specific env vars
│   ├── db/migrations/       # SQL migrations (embed.FS)
│   ├── repo/                # Data access layer
│   ├── service/             # Business logic
│   └── web/
│       ├── server.go        # Routes and middleware stack
│       ├── handler/         # One package per domain (home/, auth/, errors/)
│       └── components/      # Shared Templ components and layout
├── static/                  # CSS, JS (vendored HTMX + Alpine), images
├── docs/                    # ADRs, feature specs, AI guides
├── docker/
├── Makefile
├── AGENTS.md                # AI coding conventions
└── go.mod
```

## Stack

| Layer           | Technology                         |
|-----------------|------------------------------------|
| Language        | Go 1.25+                           |
| HTTP            | Echo v4                            |
| Templates       | Templ                              |
| Interactivity   | HTMX + Alpine.js                   |
| Database        | PostgreSQL via pgx + sqlx          |
| Migrations      | golang-migrate                     |
| Auth            | Argon2id + cookie sessions         |
| CSS             | Plain CSS design system or Tailwind |

## Architecture highlights

**Content negotiation** -- the same handler serves both HTMX (HTML) and JSON API responses. No duplicate route sets.

**Identity is a string** -- all framework packages accept subject IDs as `string`. Projects using `int64`, `uuid.UUID`, or a field called `account_id` provide their own conversion at the boundary.

**Callback-based extensibility** -- auth middleware uses a `SubjectLoader` callback, RBAC uses a `RoleChecker` callback. The framework never knows your user struct.

**Start monolith, add services later** -- `hamr add service billing` scaffolds a new service with its own `cmd/`, config, and Dockerfile. Inter-service calls propagate request ID and subject ID via `pkg/client`.

## Development

```bash
# Build the CLI
make build

# Run tests
make test

# Lint
make lint

# Vet
make vet
```

## Requirements

- Go 1.25 or later
- PostgreSQL 15+ (for generated projects)
- Docker (optional, for local Postgres via docker-compose)

## License

See [LICENSE](LICENSE) for details.
