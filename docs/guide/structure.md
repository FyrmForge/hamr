# Project structure

A scaffolded project keeps all wiring in one entrypoint, one Go package per page, and business logic in services that sit between handlers and the repository layer. This page maps the generated tree and the conventions that hold it together.

**Package references:** [Config](pkg/config.md)

## The project tree

A freshly scaffolded project (PostgreSQL + sqlx, default options) looks like this. Entries marked † depend on scaffold options:

```text
myapp/
├── cmd/
│   ├── site/
│   │   ├── main.go           # Entrypoint: config, server, DB, sessions, route registration
│   │   └── Dockerfile
│   └── migrate/main.go       # Migration CLI (up/down/status/create) † absent with --migrate-startup
├── internal/
│   ├── api/
│   │   ├── server.go         # /api group + its Deps/RegisterRoutes
│   │   └── handler/health/   # GET /api/health
│   ├── auth/cookies.go       # Session cookie helpers shared by login + register
│   ├── db/
│   │   ├── db.go             # Embeds migrations (sqlx) or runs AutoMigrate (gorm)
│   │   └── migrations/       # 001_initial.up.sql / .down.sql † sqlx only; gorm uses db/models.go
│   ├── devactions/           # hamr dev widget actions + seed.sql
│   ├── middleware/logging.go # Request ID + request logging (project-local, not hamr)
│   ├── repo/                 # Store interface, entities (user.go), DB impl in repo/postgres/ or repo/sqlite/
│   ├── service/auth.go       # Business logic — the only caller of repo/
│   └── web/
│       ├── server.go         # Site routes, middleware, Deps, static pages
│       ├── components/       # Layout, ErrorPage, StaticURL, form/ helpers, staticmanifest.go
│       └── handler/          # One package per page; .templ files sit next to handler.go
│           ├── home/         # handler.go, home.templ
│           ├── about/        # Static page (pre-rendered by --generate)
│           └── auth/
│               ├── login/    # handler.go, login.templ (also owns POST /logout)
│               └── register/
├── frontend/
│   ├── static/
│   │   ├── css/              # base/, components/, layout/, pages/ (plain CSS)
│   │   ├── images/
│   │   └── js/               # htmx, idiomorph, main.js (alpine † if opted in)
│   └── dist/                 # Fingerprinted assets (created by `hamr gen static`)
├── docker/docker-compose.yaml # Postgres (and RustFS for s3) † not generated for SQLite + local storage
├── docs/                     # adr/, features/TEMPLATE.md, llms.txt, llms-full.txt
├── e2e/                      # Browser tests † --e2e only
├── scripts/db-shell.sh       # Used by `make db-sh`
├── .github/workflows/        # ci.yml, deploy.yml
├── generated/                # Pre-rendered static pages (created by --generate)
├── hamr.toml                 # Dev server, static, and lint config
├── hamr.vendor.json          # Vendored JS checksums
├── .env / .env.example
├── Makefile
├── AGENTS.md                 # Project conventions for humans and AI assistants
└── CLAUDE.md                 # AI assistant entrypoint (points at AGENTS.md)
```

Other options add their own packages (e.g. `internal/flags/` for `--feature-flags`, `locales/` for `--locale`, `frontend/css/input.css` + `package.json` for Tailwind).

## Layers and conventions

- **`cmd/site/main.go`** — Bootstraps config, server, database, sessions and services, then calls `web.RegisterRoutes` and `api.RegisterRoutes`. All setup in one file.
- **`internal/web/server.go`** — Registers site routes and middleware. Receives a `Deps` struct: `Store`, `BaseURL`, `StaticBaseURL`, `DevMode`, `DevActions`, `SessionManager`, `AuthService`, `FileStorage`, plus `Hub`, `Flags`, `LocaleBundle`, or `EmailSender` when those options are on. See [Routing and handlers](routing.md).
- **`internal/web/handler/`** — One package per page. Each has a `handler.go` with an unexported `handler` struct and a `NewHandler` constructor, and its `.templ` files alongside with unexported components (`homePage`, `loginForm`).
- **`internal/web/components/`** — Shared templ: `Layout`, `ErrorPage`, `StaticURL`, and form helpers. See [Templates and htmx](templates.md).
- **`internal/service/`** — Business logic. Page handlers parse the request, call a service, and render; the service owns everything in between. A page handler holding a `repo.Store` is the layering bug to avoid. (The scaffolded `/api/health` check and the auth subject loader are the deliberate exceptions.)
- **`internal/repo/`** — Data access. The `Store` interface, entity types like `repo.User`, and the database implementation.
- **`internal/db/migrations/`** — Numbered, embedded SQL files, starting with `001_initial.up.sql` / `001_initial.down.sql` (sqlx). GORM projects define schema in struct tags and auto-migrate via `internal/db/models.go`. See [Database](database.md).

## Configuration with `hamr.toml`

The `hamr.toml` file configures `hamr dev`, `hamr gen static`, and `hamr lint templ`. Trimmed from a PostgreSQL scaffold:

```toml title="hamr.toml"
[hamr]
version = "0.45.1"          # hamr version that scaffolded the project
scaffolded_at = "2026-01-01"

[options]                   # scaffold choices, read by `hamr add`
database = "postgres"
db_connector = "sqlx"
auth = "session"
css = "plain"
# websockets, e2e, stripe, locale, storage, alpine ...

[static]                    # hamr gen static
dir = "frontend/static"
dist = "frontend/dist"
manifest = "internal/web/components/staticmanifest.go"
package = "components"

[dev]
proxy_target = "$PORT"      # app port, read from .env; proxy listens on localhost:3000
inject_reload = true

[[dev.docker_compose]]      # postgres or s3 projects only
name = "deps"
file = "docker/docker-compose.yaml"
wait_ready = true

[[dev.watch]]
name = "templ"
watch = "**/*.templ"
ignore = "*_templ.go"
cmd = "templ generate"
debounce = 100

[[dev.watch]]
name = "site"
watch = ["**/*.go", "**/*.templ", ".env"]
ignore = ["*_templ.go", "*_test.go"]
cmd = "go build -o ./bin/site ./cmd/site && ./bin/site --generate"
run = "./bin/site"
depends = ["templ"]
reload = "full"

[[dev.watch]]
name = "static"
watch = ["frontend/static/**/*"]
cmd = "hamr gen static"
debounce = 200

[lint.templ]                # hamr lint templ / make templint
inline-if = "error"
img-alt   = "warning"
# ...
```

Options add sections such as `[dev.email]`, `[dev.stripe]`, `[locale]`, a `locale` watch rule, or a `sync-static` daemon.

Anything you want changed only on your own machine goes in a gitignored
`.pref.hamr.toml` next to `hamr.toml` — it is merged over the committed
config per-key, so you write just the keys you're changing. See
[hamr.toml reference → `.pref.hamr.toml`](hamr-toml.md#prefhamrtoml--per-developer-overrides).

See the [hamr.toml reference](hamr-toml.md) for the full schema, or
[hamr dev](hamr-dev.md) for tutorial-style usage.

## Environment variables

HAMR uses environment variables for all runtime configuration. The `.env` file is loaded automatically via `godotenv/autoload`:

```go title="cmd/site/main.go"
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

## Makefile targets

Every generated project includes a Makefile. Targets that need the database pick up `hamr dev`'s port-walked `.env` values via `hamr env --export`.

| Target | Description |
|--------|-------------|
| `make install` | Install the `hamr` and `templ` CLIs at the versions pinned in `go.mod` |
| `make build` | `templ generate`, `hamr gen static`, build `bin/site`, then `make generate` |
| `make generate` | Pre-render static pages (`./bin/site --generate`) into `generated/` |
| `make test` | `templ generate`, then `go test ./...` |
| `make fmt` | `go fmt ./...` |
| `make lint` | Run golangci-lint |
| `make templint` | `hamr lint templ` (rules from `[lint.templ]`) |
| `make templ-check` | Fail if generated `*_templ.go` files are stale |
| `make migrate` | Run pending migrations (sqlx) or AutoMigrate (gorm) — absent with `--migrate-startup` |
| `make migrate-down` / `migrate-status` / `migrate-create NAME=...` | sqlx only, absent with `--migrate-startup` |
| `make seed` | Load `internal/devactions/seed.sql` (test accounts). Safe to re-run |
| `make db-sh` | Open a shell to the local dev database |
| `make docker-up` / `docker-down` / `docker-delete` / `docker-reload` | Manage Compose services via `hamr compose` — PostgreSQL or S3 projects only |
| `make clean` | Remove `bin/` and coverage files |

Options add more: `css-build` (Tailwind), `locale-gen` (`--locale`), `sync-static` (`--static-s3`), `e2e`, `e2e-local`, `e2e-run`, `e2e-run-local` (`--e2e`).

Prefer `make build` and `make test` over bare `go build` / `go test`: they run `templ generate` first, so you never compile against stale `*_templ.go` files.
