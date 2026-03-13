# Project Setup

HAMR's generator gives you a working app with opinionated conventions so you don't have to wire together Echo, Templ, migrations, and a dev server yourself. This guide walks through scaffolding a new project, understanding its structure, and configuring it.

**Package references:** [CLI](cli.md), [Config](pkg/config.md)

---

## Scaffolding

Create a new project with the interactive wizard:

```bash
hamr new
```

The wizard prompts for module path, CSS approach, storage backend, and optional features (WebSocket, E2E tests, Stripe). To skip prompts, pass flags directly:

```bash
hamr new myapp \
  --module github.com/user/myapp \
  --css tailwind \
  --storage s3 \
  --websocket \
  --e2e
```

To scaffold into the current directory instead of a subfolder:

```bash
hamr new . --module github.com/user/myapp
```

See [CLI Reference](cli.md#hamr-new) for all flags.

---

## Project Structure

A freshly scaffolded project looks like this:

```
myapp/
├── cmd/
│   └── site/
│       └── main.go           # Application entrypoint
├── internal/
│   └── web/
│       ├── server.go         # Route registration, deps, and static pages
│       └── handler/          # Route handlers by domain
│           └── home/
│               ├── handler.go
│               └── templates/
├── migrations/               # SQL migration files
├── static/
│   ├── css/                  # Stylesheets
│   └── js/                   # Vendored JS (htmx, alpine, idiomorph)
├── docker/
│   └── docker-compose.yaml   # Local dev services (Postgres, etc.)
├── generated/                # Pre-rendered static pages
├── hamr.toml                 # Dev server configuration
├── .env                      # Environment variables
├── Makefile                  # Build, test, lint targets
└── CLAUDE.md                 # AI assistant instructions
```

### Key conventions

- **`cmd/site/main.go`** — Bootstraps config, database, server, and starts listening. All setup in one file.
- **`internal/web/server.go`** — Registers routes and middleware on the server. Receives a `Deps` struct with shared dependencies (DB, logger, storage, etc.). See [Handlers & Routing](04-handlers-routing.md) for details.
- **`internal/web/handler/`** — One subfolder per domain (home, auth, dashboard). Each has a `handler.go` with an `Handler` struct and a `templates/` folder with templ components.
- **`migrations/`** — Numbered SQL files (`001_create_users.up.sql`, `001_create_users.down.sql`).

---

## Configuration with `hamr.toml`

The `hamr.toml` file configures the dev server. It has two main sections:

```toml
[proxy]
listen = ":3000"        # browser connects here
target = ":8080"        # your app listens here

[[dev.watch]]
name = "templ"
watch = "**/*.templ"
ignore = "*_templ.go"
cmd = "templ generate"

[[dev.watch]]
name = "go"
watch = ["**/*.go", "**/*.templ"]
ignore = ["*_templ.go", "*_test.go"]
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
depends = ["templ"]
reload = "full"
```

See [Development Workflow](02-dev-workflow.md) for the full `hamr.toml` reference.

---

## Environment Variables

HAMR uses environment variables for all runtime configuration. The `.env` file is loaded automatically via `godotenv/autoload`:

```go
import _ "github.com/joho/godotenv/autoload"
```

Use the `config` package for typed access with sensible defaults:

```go
var (
    envDBURL   = config.GetEnvOrPanic("DATABASE_URL")
    envPort    = config.GetEnvOrDefaultInt("PORT", 8080)
    envDevMode = config.GetEnvOrDefaultBool("DEV_MODE", false)
    envTimeout = config.GetEnvOrDefaultDuration("REQUEST_TIMEOUT", 30*time.Second)
)
```

- `GetEnvOrPanic` — panics at startup if the variable is missing. Use for required values like `DATABASE_URL`.
- `GetEnvOrDefault*` — returns the default on missing/invalid values. Use for optional config.

Declare config as package-level `var` blocks so values resolve once at startup.

See [Config](pkg/config.md) for the full API.

---

## Makefile Targets

Every generated project includes a Makefile with standard targets:

| Target | Description |
|--------|-------------|
| `make build` | Compile the site binary (runs templ generate + static generation) |
| `make test` | Run all tests |
| `make vet` | Vet all packages |
| `make lint` | Run golangci-lint |
| `make generate` | Generate static pages (`./bin/site --generate`) |
| `make migrate` | Run database migrations |

Always use `make` targets — never run `go build` or `go test` directly against individual packages.

---

## Next Steps

- [Development Workflow](02-dev-workflow.md) — Start the dev server with live reload
- [Database](03-database.md) — Connect to PostgreSQL and run migrations
- [Handlers & Routing](04-handlers-routing.md) — Write your first handler
