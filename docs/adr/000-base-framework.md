# ADR-000: HAMR Framework - Base Architecture & Implementation Plan

- **Status**: Accepted
- **Date**: 2026-02-28
- **Authors**: JamesTiberiusKirk

## Context

HAMR is an opinionated Go full-stack framework and project bootstrapping CLI. It extracts
proven patterns from a production Go web application (SNL) into reusable, domain-agnostic
packages. The goal: `hamr new myproject` gives you a production-ready Go + Templ + HTMX +
Alpine.js full-stack app with sensible defaults, extensible architecture, and AI-ready
documentation.

HAMR is **two things**:
1. **Framework library** (`pkg/`) - reusable Go packages projects import
2. **CLI tool** (`cmd/hamr/`) - scaffolds new projects that use the framework

## Decisions

- **CLI prompts**: Bubbletea (Charm ecosystem)
- **HTTP framework**: Echo, wrapped directly (opinionated, not swappable)
- **Package layout**: Under `pkg/` (e.g. `hamr/pkg/validate`)
- **Auth scaffold**: CLI option - user chooses between pre-built users/sessions tables or empty migration
- **Content negotiation**: Same handler serves both HTMX (HTML) and JSON API
- **Extensibility**: Interfaces + functional options + lifecycle hooks
- **WebSocket identity**: Session-based by default (works without auth), optional user-based targeting when auth is present
- **Identity is configurable**: All packages that reference "user ID" accept it as a `string` and let the project configure how it's extracted. Projects using `int64`, `uuid.UUID`, or a field called `account_id` instead of `user_id` just provide their own extraction/conversion functions

## Repository Structure

```
github.com/FyrmForge/hamr/
├── cmd/
│   └── hamr/
│       └── main.go                     # CLI entry point
├── pkg/
│   ├── config/
│   │   └── config.go                   # Env-based config helpers
│   ├── logging/
│   │   └── logging.go                  # slog wrapper (JSON/text, context-aware)
│   ├── ptr/
│   │   └── ptr.go                      # Pointer utilities (generic + concrete)
│   ├── validate/
│   │   ├── validate.go                 # Core validators + custom registration
│   │   └── messages.go                 # Error message constants
│   ├── auth/
│   │   ├── auth.go                     # Argon2id hashing, token generation
│   │   └── session.go                  # SessionStore interface + SessionManager
│   ├── db/
│   │   ├── db.go                       # Connect with retry + keep-alive
│   │   └── migrate.go                  # Migration runner (user passes embed.FS)
│   ├── htmx/
│   │   └── htmx.go                     # Request detection, response headers, OOB helpers
│   ├── respond/
│   │   ├── respond.go                  # HTML, JSON, Negotiate, Error, ValidationError
│   │   └── pagination.go              # Page struct, PagedResponse[T], ParsePagination
│   ├── ctx/
│   │   └── ctx.go                      # Typed context keys (generics-based)
│   ├── middleware/
│   │   ├── auth.go                     # Auth, RequireAuth, OptionalAuth, RequireNotAuth
│   │   ├── rbac.go                     # RequireRoles, RequireActive (callback-based)
│   │   ├── flash.go                    # Cookie-based one-time flash messages
│   │   ├── ratelimit.go               # Token bucket rate limiter
│   │   ├── requestid.go               # UUID request tracing + structured logging
│   │   ├── cache.go                    # Per-asset-type cache headers
│   │   ├── audit.go                    # Mutation audit logging (AuditLogger interface)
│   │   ├── csrf.go                     # CSRF config helper for Echo
│   │   └── cors.go                     # CORS config helper
│   ├── server/
│   │   ├── server.go                   # Echo wrapper with functional options
│   │   ├── options.go                  # All WithXxx option functions
│   │   └── hooks.go                    # Lifecycle hooks (OnStart, OnShutdown, etc.)
│   ├── storage/
│   │   ├── storage.go                  # FileStorage + SignableStorage interfaces
│   │   ├── local.go                    # Local filesystem implementation
│   │   └── s3.go                       # S3/R2/MinIO implementation
│   ├── janitor/
│   │   └── janitor.go                  # Task interface + scheduler
│   ├── notify/
│   │   ├── sender.go                   # Sender interface (Recipient, Message)
│   │   ├── dispatcher.go              # Async wrapper
│   │   └── noop.go                     # No-op for testing/dev
│   └── websocket/
│       ├── hub.go                      # Session+room-based connection registry
│       ├── client.go                   # Read/write pumps, heartbeat
│       ├── event.go                    # Event types (HTML direct, HTMX trigger, data)
│       └── emitter.go                  # Clean send API
├── internal/
│   └── cli/
│       ├── cmd/
│       │   ├── root.go                 # Cobra root command
│       │   ├── new.go                  # `hamr new` command
│       │   └── version.go             # `hamr version`
│       ├── prompt/
│       │   └── prompt.go              # Bubbletea interactive prompts
│       ├── generator/
│       │   ├── generator.go           # Project generation orchestrator
│       │   └── files.go               # Template execution + file writing
│       └── templates/                  # Embedded text/template files
│           ├── cmd/server/
│           ├── internal/
│           ├── static/
│           ├── docs/
│           ├── docker/
│           └── root/                   # .gitignore, Makefile, AGENTS.md, etc.
├── go.mod
├── go.sum
└── README.md
```

## Phase 1: Foundation Packages (no internal deps)

### 1. `pkg/config/config.go`
- `LoadEnvFile(paths ...string) error` - wraps godotenv
- `GetEnvOrDefault(key, def string) string`
- `GetEnvOrPanic(key string) string`
- `GetEnvOrDefaultInt(key string, def int) int`
- `GetEnvOrDefaultBool(key string, def bool) bool`
- `GetEnvOrDefaultDuration(key string, def time.Duration) time.Duration`

### 2. `pkg/logging/logging.go`
- `New(production bool) *slog.Logger` - JSON handler in prod, tint in dev
- `FromContext(ctx) *slog.Logger` - get logger from context
- `WithLogger(ctx, logger) context.Context` - set logger in context
- `With(ctx, ...any) context.Context` - add attrs to context logger

### 3. `pkg/ptr/ptr.go`
- Generic: `To[T](v T) *T`, `From[T](p *T) T`, `FromOr[T](p *T, def T) T`
- Concrete: `String`, `StringVal`, `Bool`, `Int`, `Deref`, `DerefInt`, `DerefBool`, `IntToStr`, `BoolToYesNo`

### 4. `pkg/validate/validate.go` + `messages.go`
- Core validators (return `""` for valid, error message for invalid):
  `Required`, `Email`, `Phone`, `URL`, `MinLength`, `MaxLength`, `Match`,
  `OneOf`, `IntRange`, `MinAge`, `MaxAge`, `PasswordStrength`
- `*Msg` variants for custom error messages
- `CheckPasswordRequirements(password) []PasswordRequirement` for UI
- `NormalizeURL(value) string`
- Custom validator registry: `Register(name, fn)`, `Run(name, value) string`
- All `Msg*` constants in messages.go

## Phase 2: Data + Auth Packages

### 5. `pkg/auth/auth.go`
- `HashPassword(password string) (string, error)` - Argon2id
- `HashPasswordWithConfig(password string, cfg HashConfig) (string, error)`
- `CheckPassword(password, encodedHash string) (bool, error)`
- `GenerateToken() (string, error)` - 32-byte base64url
- `GenerateTokenN(n int) (string, error)`

### 6. `pkg/auth/session.go`
- `SessionStore` interface: `Create`, `GetByToken`, `Delete`, `DeleteBySubjectID`
- `Session` struct: `ID`, `SubjectID` (string — the project's user/account/member ID, converted to string), `Token`, `ExpiresAt`, `CreatedAt`, `Metadata map[string]any`
- `SessionManager` with functional options: `NewSessionManager(store, ...SessionOption)`
- `SessionConfig`: Duration (7d default), CookieName, CookiePath, CookieSecure, SameSite
- Methods: `CreateSession`, `ValidateSession`, `DeleteSession`, `DeleteSubjectSessions`
- Note: `SubjectID` is always a `string` internally — projects convert their native ID type (int64, UUID, etc.) at the boundary

### 7. `pkg/db/db.go`
- `Connect(databaseURL string, opts ...ConnectOption) (*sqlx.DB, error)` - retry with backoff
- `ConnectConfig`: MaxOpenConns(10), MaxIdleConns(5), ConnMaxIdleTime(5s), ConnMaxLifetime(1m), MaxRetries(3)
- `StartKeepAlive(db *sqlx.DB, interval time.Duration, poolSize int)`

### 8. `pkg/db/migrate.go`
- `Migrate(db *sqlx.DB, cfg MigrateConfig) error` - user passes `embed.FS`
- `MigrateDown(db *sqlx.DB, cfg MigrateConfig) error`
- `MigrateConfig`: FS, Directory, Driver

## Phase 3: HTTP Layer Packages

### 9. `pkg/htmx/htmx.go`
Request detection:
- `IsHTMX(r *http.Request) bool`
- `IsBoosted(r *http.Request) bool`
- `GetTrigger(r) string`, `GetTarget(r) string`

Response headers:
- `Redirect(w, url)`, `Trigger(w, ...events)`, `TriggerAfterSettle(w, ...events)`
- `TriggerAfterSwap(w, ...events)`, `Reswap(w, strategy)`, `Retarget(w, selector)`
- `Refresh(w)`, `PushURL(w, url)`, `ReplaceURL(w, url)`

### 10. `pkg/respond/respond.go`
- `HTML(c echo.Context, status int, component templ.Component) error`
- `JSON(c echo.Context, status int, data any) error`
- `Negotiate(c echo.Context, status int, jsonData any, component templ.Component) error`
  - Checks `Accept` header + `HX-Request` header to pick format
- `Error(c echo.Context, status int, msg string, component ...templ.Component) error`
  - JSON: `{"error": "msg", "code": 422}`
  - HTML: renders error component or default
- `ValidationError(c echo.Context, fields map[string]string, component ...templ.Component) error`
  - JSON: `{"error": "validation failed", "fields": {"email": "required"}}`
  - HTML: renders OOB validation component

### 11. `pkg/respond/pagination.go`
- `Page` struct: Number, Size, Total, TotalPages, HasNext, HasPrev
- `PagedResponse[T]` struct: Data []T, Page
- `ParsePagination(c echo.Context, defaultSize int) (page, size int)`
- `NewPage(page, size, total int) Page`

### 12. `pkg/ctx/ctx.go`
- `Key[T]` type for type-safe context values
- `NewKey[T](name string) Key[T]`
- `Set[T](c echo.Context, key Key[T], value T)`
- `Get[T](c echo.Context, key Key[T]) (T, bool)`
- `MustGet[T](c echo.Context, key Key[T]) T`
- Pre-defined keys: `SubjectIDKey`, `SubjectKey`, `SessionKey`, `RequestIDKey`, `FlashKey`

## Phase 4: Middleware Package

### 13. `pkg/middleware/auth.go`
- `SubjectLoader func(ctx context.Context, subjectID string) (any, error)` - the key abstraction; loads whatever the project calls a "user" by their ID
- `AuthConfig`: SessionManager, SubjectLoader, CookieName, LoginRedirect, HomeRedirect
- `Auth(cfg)`, `RequireAuth(cfg)`, `OptionalAuth(cfg)`, `RequireNotAuth(cfg)`
- `GetSubject(c echo.Context) any`, `GetSubjectID(c echo.Context) string`
- Projects type-assert the `any` return to their own user/account struct

### 14. `pkg/middleware/rbac.go`
- `RoleChecker func(user any, roles []string) bool`
- `ActiveChecker func(user any) bool`
- `RequireRoles(checker RoleChecker, roles ...string) echo.MiddlewareFunc`
- `RequireActive(checker ActiveChecker) echo.MiddlewareFunc`

### 15. `pkg/middleware/flash.go`
- `FlashType` (info, success, warning, error)
- `FlashMessage` struct
- `Flash() echo.MiddlewareFunc` - reads cookie, sets context, clears cookie
- `SetFlash(c, message, flashType)`, `GetFlash(c) *FlashMessage`

### 16. `pkg/middleware/ratelimit.go`
- `RateLimitConfig`: RatePerMinute(60), Burst(10), ExpiresIn, SkipPaths
- `RateLimit() echo.MiddlewareFunc` - defaults
- `RateLimitWithConfig(cfg) echo.MiddlewareFunc`

### 17. `pkg/middleware/requestid.go`
- Generates UUID or uses `X-Request-ID` header
- Adds to slog context with client IP
- Logs request method, status, duration

### 18. `pkg/middleware/cache.go`
- `CacheControl(disableCaching bool) echo.MiddlewareFunc`
- Immutable assets (images, fonts): 1 year
- Static assets (CSS, JS): 1 day
- Configurable via functional options

### 19. `pkg/middleware/audit.go`
- `AuditLogger` interface: `Log(ctx, *AuditEntry) error`
- `AuditEntry`: ActorID (string, from session subject or empty), Action, EntityType, Data, Timestamp
- `AuditConfig`: Logger, `ActorIDFunc func(c echo.Context) string` (defaults to `GetSubjectID`, override for custom identity extraction)
- `Audit(logger AuditLogger) echo.MiddlewareFunc` - logs non-GET requests
- `AuditWithConfig(cfg AuditConfig) echo.MiddlewareFunc`

### 20. `pkg/middleware/csrf.go` + `cors.go`
- Helper functions wrapping Echo's built-in CSRF/CORS with framework defaults
- `CSRFConfig`: CookieName, TokenLookup (`form:csrf_token,header:X-CSRF-Token`), SkipPaths
- CSRF skips paths containing `/api/` by default (API uses Bearer tokens)

## Phase 5: Server + Infrastructure Packages

### 21. `pkg/server/server.go` + `options.go` + `hooks.go`
- `Server` wrapping Echo with functional options
- `New(opts ...Option) *Server`
- Options: `WithHost`, `WithPort`, `WithDevMode`, `WithMiddleware`, `WithStaticDir`,
  `WithEmbeddedStatic`, `WithErrorHandler`
- Route methods: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `Group`
- `Echo() *echo.Echo` for escape hatch
- `Start() error`, `Shutdown(ctx) error`
- Hooks: `WithOnServerStart(fn)`, `WithOnShutdown(fn)`, `WithOnBeforeMigrate(fn)`, `WithOnAfterMigrate(fn)`

### 22. `pkg/janitor/janitor.go`
- `Task` interface: `Name() string`, `Run(ctx) (int64, error)`
- `New(interval, ...Option) *Janitor`
- `AddTask(task)`, `Start()`, `Stop()`
- Options: `WithTimeout(d)`, `WithRunImmediately(bool)`, `WithLogger(*slog.Logger)`

### 23. `pkg/storage/` (storage.go, local.go, s3.go)
- `FileStorage` interface: `Save`, `Read`, `Delete`, `Exists`
- `SignableStorage` extends with `SignURL`
- `NewLocalStorage(basePath string) *LocalStorage`
- `NewS3Storage(cfg S3Config) *S3Storage`

### 24. `pkg/notify/` (sender.go, dispatcher.go, noop.go)
- `Sender` interface: `Send(ctx, Recipient, Message) (*SendResult, error)`
- `Recipient`: Address, Name, Meta
- `Message`: Subject, Body, HTMLBody
- `NewAsyncDispatcher(sender, timeout) *AsyncDispatcher`
- `NewNoopSender(name) *NoopSender`

### 25. `pkg/websocket/` (hub.go, client.go, event.go, emitter.go)
- `Hub` with session-based + room-based routing (works without auth)
- Primary index by session ID, optional secondary index by subject ID (when auth present)
- `NewHub(...HubOption) *Hub`
- `HubOption`: `WithSubjectIDFunc(func(r *http.Request) string)` - configures how subject identity is extracted (defaults to session cookie, auth middleware can override to return the authenticated subject ID)
- `SendToSession(sessionID, msg)`, `SendToRoom(room, msg)`, `Broadcast(msg)`
- `SendToSubject(subjectID, msg)` - targets all sessions for a given subject, only works when auth has mapped session→subject, no-ops otherwise
- `JoinRoom(client, room)`, `LeaveRoom(client, room)`
- `Client`: SessionID, SubjectID (optional, set when auth maps it), Rooms, Meta, Send channel, ReadPump, WritePump
- `AssociateSubject(sessionID, subjectID)` - called by auth middleware to enable subject-based targeting
- `Event` types: `NewEvent`, `NewHTMLEvent`, `NewTriggerEvent`
- `Emitter`: `ToSession`, `ToSubject` (auth-only), `ToRoom`, `ToRoomExcept`, `Broadcast`

## Phase 6: CLI Scaffolding Tool

### 26. CLI Structure
```
cmd/hamr/main.go                    # cobra root
internal/cli/cmd/root.go            # root command
internal/cli/cmd/new.go             # `hamr new <name>`
internal/cli/cmd/version.go         # `hamr version`
internal/cli/prompt/prompt.go       # bubbletea prompts
internal/cli/generator/generator.go # orchestrator
internal/cli/generator/files.go     # template exec + file write
internal/cli/templates/             # embedded text/template files
```

### 27. `hamr new` Interactive Options
1. **Project name** (from arg)
2. **Go module path** (flag `--module` or prompt)
3. **CSS approach**: Plain CSS with design system | Tailwind CSS
4. **Auth scaffolding**: Yes (with users/sessions tables) | Yes (empty migration) | No
5. **File storage**: Yes (local + S3) | No
6. **WebSocket support**: Yes | No
7. **Notification system**: Yes | No

### 28. Template Data Model
```go
type ProjectConfig struct {
    Name           string   // "myproject"
    Module         string   // "github.com/user/myproject"
    CSS            string   // "plain" | "tailwind"
    IncludeAuth    bool
    AuthWithTables bool     // generate users/sessions migration
    IncludeStorage bool
    IncludeWS      bool
    IncludeNotify  bool
    Database       string   // "postgres"
    GoVersion      string   // "1.25"
}
```

### 29. Generated Project Structure
```
<project>/
├── cmd/server/
│   ├── main.go                 # Bootstrap: config, db, migrate, services, server
│   └── Dockerfile
├── internal/
│   ├── config/config.go        # App-specific env vars
│   ├── db/
│   │   ├── db.go               # Uses hamr/pkg/db
│   │   └── migrations/
│   │       ├── 001_initial.up.sql      # (if auth+tables: users, sessions, password_resets)
│   │       └── 001_initial.down.sql
│   ├── repo/
│   │   ├── repo.go             # Store interface
│   │   ├── user.go             # User model (if auth)
│   │   └── postgres/
│   │       ├── store.go
│   │       └── users.go        # (if auth)
│   ├── service/
│   │   └── auth.go             # (if auth)
│   └── web/
│       ├── server.go           # Routes, middleware stack
│       ├── handler/
│       │   ├── home/           # handler.go + templates.templ
│       │   ├── health/         # handler.go (JSON health check)
│       │   ├── auth/           # (if auth) handler.go + templates.templ + validation.go
│       │   └── errors/         # handler.go + templates.templ
│       └── components/
│           ├── layout.templ    # HTMX config, CSRF injection, response handling config
│           ├── helpers.go      # Template helper functions
│           └── form/
│               ├── fields.templ    # FieldError, FieldErrorOOB, CSRFField
│               └── helpers.go      # GetError, IsSelected
├── static/
│   ├── css/                    # (if plain CSS)
│   │   ├── base/               #   variables.css, reset.css, utilities.css
│   │   ├── components/         #   buttons.css, forms.css, alerts.css
│   │   ├── layout/             #   header.css, footer.css
│   │   └── pages/              #   home.css
│   ├── js/
│   │   ├── htmx.min.js        # Vendored
│   │   ├── alpine.min.js       # Vendored
│   │   └── main.js             # HTMX config events, component init, afterSwap
│   └── images/
├── docs/
│   ├── adr/
│   │   └── 000-base-framework.md
│   ├── features/
│   │   └── TEMPLATE.md
│   └── ai-guides/
│       ├── validation.md
│       ├── forms.md
│       ├── handler-patterns.md
│       └── css.md              # (or tailwind.md)
├── docker/docker-compose.yaml
├── scripts/
├── .env.example
├── .gitignore
├── Makefile                    # dev, build, test, lint, docker-up, docker-down, seed, migrate
├── AGENTS.md                   # AI coding rules + project conventions
├── CLAUDE.md                   # Claude-specific development guidelines
├── tailwind.config.js          # (if tailwind)
├── package.json                # (if tailwind)
├── README.md
└── go.mod
```

### 30. Key Generated Files Content

**layout.templ** - includes the critical HTMX configuration:
```javascript
htmx.config.responseHandling = [
  {code:"204", swap: false},
  {code:"[23]..", swap: true},
  {code:"400", swap: true, error: false},
  {code:"422", swap: true, error: false},
  {code:"429", swap: true, error: false},
  {code:"[45]..", swap: false, error: true}
];
document.addEventListener('htmx:configRequest', function(evt) {
  var csrfToken = document.querySelector('input[name="csrf_token"]');
  if (csrfToken) evt.detail.headers['X-CSRF-Token'] = csrfToken.value;
});
```

**main.js** - HTMX event listeners:
- `htmx:responseError` logging
- `htmx:afterSwap` component re-initialization
- `htmx:afterSwap` scroll-to-first-error on form re-render

**AGENTS.md** - AI coding rules:
- Project structure and conventions
- Handler pattern (one per domain, constructor injection, `// GET /path` comments)
- Validation pattern (two-level: blur field + submit form)
- Response pattern (Negotiate for dual HTML/JSON)
- Error handling conventions (422 for validation, 303 for redirects)
- CSS architecture (if plain CSS)
- Testing conventions

**AI guides** - step-by-step instructions:
- validation.md: How to add field validation with HTMX OOB swaps
- forms.md: How to create forms (handler, templ, validation endpoint, CSRF)
- handler-patterns.md: How to add handlers for both HTML and JSON
- css.md / tailwind.md: How CSS is organized and how to add styles

## Phase 7: Post-MVP

- Bearer token auth middleware (dual auth for JSON API)
- JSON-specific error handler middleware
- WebSocket PG LISTEN/NOTIFY integration (optional package)
- `hamr generate handler <name>` - generate individual handlers
- `hamr generate migration <name>` - generate migration pair
- `hamr dev` - dev server with hot reload (wraps air/reflex)
- SQLite support as DB option
- GoReleaser config for distribution

## Key Interfaces (Extensibility Points)

| Interface         | Package          | Purpose                                                         |
|-------------------|------------------|-----------------------------------------------------------------|
| `SessionStore`    | `pkg/auth`       | Pluggable session persistence (postgres, redis, memory)         |
| `FileStorage`     | `pkg/storage`    | Pluggable file backend (local, S3, R2, GCS)                     |
| `SignableStorage` | `pkg/storage`    | Storage with URL signing                                        |
| `Task`            | `pkg/janitor`    | Custom background cleanup tasks                                 |
| `Sender`          | `pkg/notify`     | Pluggable notification channel (email, SMS, Slack)              |
| `AuditLogger`     | `pkg/middleware` | Pluggable audit persistence                                     |
| `SubjectLoader`   | `pkg/middleware` | App-specific subject (user/account/member) loading from session |
| `RoleChecker`     | `pkg/middleware` | App-specific role checking                                      |
| `ActiveChecker`   | `pkg/middleware` | App-specific account status checking                            |

## External Dependencies

| Dependency | Package | Purpose |
|------------|---------|---------|
| `labstack/echo/v4` | server, middleware, respond, ctx | HTTP framework |
| `a-h/templ` | respond | Template rendering |
| `jmoiron/sqlx` | db | SQL query helper |
| `jackc/pgx/v5` | db | PostgreSQL driver |
| `golang-migrate/migrate/v4` | db | Migration runner |
| `joho/godotenv` | config | .env file loading |
| `google/uuid` | middleware, auth | UUID generation |
| `lmittmann/tint` | logging | Colored dev logs |
| `gorilla/websocket` | websocket | WebSocket connections |
| `golang.org/x/crypto` | auth | Argon2id |
| `golang.org/x/time` | middleware | Rate limiting |
| `aws/aws-sdk-go-v2` | storage | S3 |
| `spf13/cobra` | cmd/hamr | CLI framework |
| `charmbracelet/bubbletea` | internal/cli | Interactive prompts |
| `charmbracelet/lipgloss` | internal/cli | CLI styling |

## Implementation Order

**Sprint 1** - Foundation (no deps between these):
1. `pkg/config`
2. `pkg/logging`
3. `pkg/ptr`
4. `pkg/validate`

**Sprint 2** - Data + Auth:
5. `pkg/auth`
6. `pkg/db`

**Sprint 3** - HTTP Layer:
7. `pkg/htmx`
8. `pkg/respond`
9. `pkg/ctx`

**Sprint 4** - Middleware:
10. `pkg/middleware` (all files, built incrementally)

**Sprint 5** - Server + Infrastructure:
11. `pkg/server`
12. `pkg/janitor`
13. `pkg/storage`
14. `pkg/notify`
15. `pkg/websocket`

**Sprint 6** - CLI:
16. CLI structure (cobra + bubbletea)
17. Template files for generated project
18. Generator logic
19. End-to-end test: `hamr new testproject`

**Sprint 7** - Polish:
20. Tests for all packages
21. README and documentation
22. CI/CD setup

## Verification

After each sprint:
- All packages compile: `go build ./...`
- All tests pass: `go test ./...`
- Linting passes: `golangci-lint run`

After Sprint 6 (CLI complete):
- Run `go run ./cmd/hamr new testproject --module github.com/test/testproject`
- Verify generated project compiles: `cd testproject && go build ./...`
- Verify generated project runs: `go run ./cmd/server`
- Verify templ generates: `templ generate`
- Verify all docs exist (adr/, features/, ai-guides/, AGENTS.md, CLAUDE.md)
- Verify Makefile targets work
- Verify docker-compose starts database
