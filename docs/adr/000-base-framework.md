# ADR-000: HAMR Framework - Base Architecture & Implementation Plan

- **Status**: Accepted
- **Date**: 2026-02-28
- **Authors**: JamesTiberiusKirk

## Context

HAMR is an opinionated Go full-stack framework and project bootstrapping CLI. It extracts
proven patterns from a production Go web application into reusable, domain-agnostic
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
- **Subject is `any`**: `SubjectLoader`, `GetSubject`, `RoleChecker`, and `ActiveChecker` all use `any` for the subject/user type. The framework cannot know a project's user struct at compile time. Projects type-assert once at the handler boundary. This is a deliberate tradeoff — type safety within the project, flexibility at the framework boundary
- **Always start monolith** — `hamr new` never asks about architecture
- **Add services later** — `hamr add service <name>` scaffolds a new service
- **HAMR provides tools, not opinions on project layout** — where shared types live, whether to restructure the monolith, shared DB vs separate DB — all project decisions
- **Auth propagation**: Gateway forwards subject ID via trusted header
- **E2E testing**: Go-rod + Testcontainers, fully containerized, `//go:build e2e` isolated. HAMR provides reusable helpers in `pkg/e2e`, generated projects get a ready-to-run `e2e-go/` scaffolding

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
│   │   ├── cors.go                     # CORS config helper
│   │   ├── subject.go                  # GetSubjectID/GetSubject shared helpers
│   │   └── trusted.go                  # Trusted internal subject extraction from header
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
│   ├── e2e/
│   │   ├── browser.go                  # Go-rod browser setup + timeout-safe helpers
│   │   ├── assert.go                   # Element/URL/text assertions
│   │   └── htmx.go                     # HTMX-aware waiters (idle, swap, settle)
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
- `GetEnvOrDefault(key, def string) string`
- `GetEnvOrPanic(key string) string`
- `GetEnvOrDefaultInt(key string, def int) int`
- `GetEnvOrDefaultBool(key string, def bool) bool`
- `GetEnvOrDefaultDuration(key string, def time.Duration) time.Duration`
- `.env` loading via `_ "github.com/joho/godotenv/autoload"` in the consuming `main` package

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
- `Session` struct: `ID`, `SubjectID` (string — the project's user/account/member ID, converted to string; empty for anonymous sessions), `Token`, `ExpiresAt`, `CreatedAt`, `Metadata map[string]any`
- `SessionManager` with functional options: `NewSessionManager(store, ...SessionOption)`
- `SessionConfig`: Duration (7d default), CookieName, CookiePath, CookieSecure, SameSite
- Methods: `CreateSession`, `ValidateSession`, `DeleteSession`, `DeleteSubjectSessions`
- `MemorySessionStore` — in-memory `SessionStore` implementation (`sync.RWMutex` + `map`). For dev and testing, not production. Auto-expires stale sessions on read.
- Note: `SubjectID` is always a `string` internally — projects convert their native ID type (int64, UUID, etc.) at the boundary
- Note: Sessions work without user accounts — `SubjectID` is empty for anonymous sessions (flash, CSRF, cart, preferences). Auth middleware optionally associates a subject later.

**Reference migration** (generated by `hamr new` when sessions enabled):
```sql
CREATE TABLE sessions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_id  TEXT,
    token       TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata    JSONB
);
CREATE INDEX idx_sessions_token ON sessions (token);
CREATE INDEX idx_sessions_subject_id ON sessions (subject_id) WHERE subject_id IS NOT NULL;
```

### 7. `pkg/db/db.go`
- `Connect(databaseURL string, opts ...ConnectOption) (*sqlx.DB, error)` - retry with backoff
- `ConnectConfig`: MaxOpenConns(10), MaxIdleConns(5), ConnMaxIdleTime(5s), ConnMaxLifetime(1m), MaxRetries(3)
- `StartKeepAlive(ctx context.Context, db *sqlx.DB, interval time.Duration, poolSize int)`

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
- `RateLimitConfig`: RatePerMinute(60), Burst(10), KeyFunc, Store
- `RateLimit() echo.MiddlewareFunc` - defaults (PG store)
- `RateLimitWithConfig(cfg) echo.MiddlewareFunc`
- `RateLimitStore` interface: `Allow(ctx context.Context, key string, rate int, window time.Duration) (allowed bool, remaining int, resetAt time.Time, err error)`
- `NewPGStore(db *sql.DB) RateLimitStore` — PostgreSQL `UNLOGGED` table, works across multiple instances without adding Redis (MVP default)
- `NewMemoryStore() RateLimitStore` — in-process `sync.Map`, for dev/testing only
- PG store uses an `UNLOGGED` table (`_rate_limits`) with `ON CONFLICT ... DO UPDATE` for atomic increment + window expiry; periodic cleanup via janitor task
- Migration ships with the middleware package (auto-created when PG store is used)

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
- `CSRFConfig`: CookieName, TokenLookup (`form:csrf_token,header:X-CSRF-Token`)
- `CORSConfig`: AllowOrigins, AllowMethods, AllowHeaders
- Middleware is group-agnostic — no hardcoded path skips. The generated project wires each middleware to the appropriate route group

### 21. `pkg/middleware/subject.go`
- `GetSubjectID(c echo.Context) string`, `GetSubject(c echo.Context) any` — shared helpers used by both session-based and trusted auth

### 22. `pkg/middleware/trusted.go`
- `TrustedSubject()` middleware — reads `X-Subject-ID` header, sets in context
- Same `GetSubjectID(c)` API as session-based auth — handlers don't care how auth was resolved
- Only for internal network, never exposed publicly

## Phase 5: Server + Infrastructure Packages

### 23. `pkg/server/server.go` + `options.go` + `hooks.go`
- `Server` wrapping Echo with functional options
- `New(opts ...Option) *Server`
- Options: `WithHost`, `WithPort`, `WithDevMode`, `WithMiddleware`, `WithStaticDir`,
  `WithEmbeddedStatic`, `WithErrorHandler`, `WithTimeout`, `WithMaxBodySize`
- Route methods: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `Group`
- `Echo() *echo.Echo` for escape hatch
- `Start() error`, `Shutdown(ctx) error`
- Hooks: `WithOnBeforeMigrate(fn)`, `WithOnAfterMigrate(fn)`
- **Production defaults** (enabled unless overridden):
  - Panic recovery (`middleware.Recover()`)
  - Request timeout: 30s
  - Max request body: 2MB
  - Security headers: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-XSS-Protection: 0` (rely on CSP), `Content-Security-Policy: default-src 'self'` (overridable)
- **Route group convention** (generated in project's `web/server.go`, not hardcoded in framework):
  ```
  Global  (all routes) → recovery, request ID, logging, audit
  Site    (/)          → sessions, CSRF, flash, cache, secure headers
  API     (/api/)      → CORS, rate limit, bearer auth (when added)
  ```
  Middleware packages are group-agnostic — they don't skip paths internally. The project decides which middleware applies to which group.

  **Note:** Echo's `e.Group("")` correctly isolates middleware — site middleware does NOT bleed into `/api` or `/webhooks` routes. One edge case: `Group.Use()` auto-registers `RouteNotFound` catch-alls, which can turn 405 (Method Not Allowed) into 404 responses for unregistered methods. This is a known Echo behavior, not a bug.

### 24. `pkg/janitor/janitor.go`
- `Task` interface: `Name() string`, `Run(ctx) (int64, error)`
- `New(interval, ...Option) *Janitor`
- `AddTask(task)`, `Start()`, `Stop()`
- Options: `WithTimeout(d)`, `WithRunImmediately(bool)`, `WithLogger(*slog.Logger)`

### 25. `pkg/storage/` (storage.go, local.go, s3.go)
- `FileStorage` interface: `Save`, `Read`, `Delete`, `Exists`
- `SignableStorage` extends with `SignURL`
- `NewLocalStorage(basePath string) *LocalStorage`
- `NewS3Storage(cfg S3Config) *S3Storage`

### 26. `pkg/websocket/` (hub.go, client.go, event.go, emitter.go)
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

### 27. `pkg/e2e/` (browser.go, assert.go, htmx.go)

Reusable go-rod helpers for E2E testing. Projects import `hamr/pkg/e2e` in their
`e2e-go/` test files. All operations are timeout-safe — **no `Must*` methods**.

**Configuration** — `BrowserConfig` with functional options or env var overrides:

| Option | Env Var | Default | Description |
|--------|---------|---------|-------------|
| `WithHeadless(bool)` | `E2E_HEADLESS` | `true` | `false` for local debugging with visible browser |
| `WithSlowMotion(d)` | `E2E_SLOW_MOTION` | `0` | Delay between actions (useful with headed mode) |
| `WithTimeout(d)` | `E2E_TIMEOUT` | `10s` | Default timeout for all browser operations |
| `WithArtifactDir(path)` | `E2E_ARTIFACT_DIR` | `testdata/e2e-artifacts` | Where failure screenshots/HTML/logs are saved |
| `WithScreenshotOnFailure(bool)` | `E2E_SCREENSHOT_ON_FAIL` | `true` | Auto-capture PNG on test failure |
| `WithHTMLDumpOnFailure(bool)` | `E2E_HTML_DUMP_ON_FAIL` | `true` | Auto-capture page DOM on test failure |

Env vars take precedence over code options so CI can override without code changes.

**`browser.go` — Browser lifecycle + element interaction:**
- `SetupBrowser(t, ...BrowserOption) *rod.Browser` — launches Chromium with config (headless by default, no-sandbox, disable-gpu, disable-dev-shm-usage)
- `NewPage(t, browser, url) *rod.Page` — navigates, waits for load, registers `t.Cleanup` to capture failure artifacts
- `Login(t, page, email, password string)` — fills email/password inputs, clicks submit, waits for navigation
- `WaitForElement(t, page, selector, timeout) *rod.Element` — timeout-safe element wait
- `WaitForURLChange(t, page, currentURL, timeout)` — polls page info URL until it changes
- `Input(t, page, selector, value)` — finds element and types value
- `Click(t, page, selector)` — finds element and clicks
- `SelectOption(t, page, selector, value)` — selects dropdown option by value
- `ElementExists(t, page, selector) bool` — non-fatal existence check
- `SaveScreenshot(t, page, name)` — saves PNG to artifact dir
- `SavePageHTML(t, page, name)` — dumps DOM to artifact dir

**`assert.go` — Test assertions:**
- `AssertElementExists(t, page, selector)` — fails test if element not found
- `AssertElementNotVisible(t, page, selector)` — visibility check
- `AssertElementContainsText(t, page, selector, text)` — text content match
- `AssertURL(t, page, expected)` — exact URL match
- `AssertURLContains(t, page, substring)` — partial URL match

**`htmx.go` — HTMX-aware waiters:**
- `WaitForHTMXIdle(t, page, timeout)` — waits until no in-flight htmx requests (`htmx.xhr && htmx.xhr.length === 0` or no `htmx-request` class on body)
- `WaitForHTMXSwap(t, page, selector, timeout)` — waits for element content to change after an htmx swap
- `ClickAndWaitHTMX(t, page, selector, timeout)` — clicks an element and waits for htmx to settle

**Failure artifact capture:**
- `NewPage` registers a `t.Cleanup` that checks `t.Failed()` — if true, auto-saves screenshot + HTML dump
- Artifacts named `<TestName>_<timestamp>.png` / `.html` for easy CI artifact upload
- Disabled in headed mode by default (you can see the browser)

**Critical patterns (enforced by all helpers):**
```go
// ✅ Always timeout-safe
element, err := page.Timeout(cfg.Timeout).Element("#selector")
require.NoError(t, err)

// ❌ Never use — can hang forever
page.MustElement("#selector")
```

## Phase 6: CLI Scaffolding Tool

### 30. CLI Structure
```
cmd/hamr/main.go                    # cobra root
internal/cli/cmd/root.go            # root command
internal/cli/cmd/new.go             # `hamr new <name>`
internal/cli/cmd/add.go             # `hamr add service <name>`
internal/cli/cmd/vendor.go           # `hamr vendor`
internal/cli/cmd/version.go         # `hamr version`
internal/cli/prompt/prompt.go       # bubbletea prompts
internal/cli/generator/generator.go # orchestrator
internal/cli/generator/files.go     # template exec + file write
internal/cli/templates/             # embedded text/template files
```

### 31. `hamr new` Interactive Options
1. **Project name** (from arg)
2. **Go module path** (flag `--module` or prompt)
3. **CSS approach**: Plain CSS with design system | Tailwind CSS
4. **Sessions?** Yes/No (cookie-based session management)
5. **File storage?** Yes/No — if yes, follow-up: Local folder | S3 (MinIO)
6. **S3 static watcher?** (if S3) Yes/No — watches `static/` and syncs to S3 during dev
7. **WebSocket?** Yes/No
8. **Notifications?** Yes/No
9. **E2E testing?** Yes/No
10. **Auth scaffolding** (if sessions): Full | Empty | None

Each feature is an individual yes/no confirm prompt (not a multiselect).

CLI flags:
- `--storage none|local|s3` (default: `"none"`)
- `--s3-watcher` (default: `false`, only meaningful with `--storage s3`)

### 32. Template Data Model
```go
type ProjectConfig struct {
    Name              string   // "myproject"
    Module            string   // "github.com/user/myproject"
    CSS               string   // "plain" | "tailwind"
    IncludeSessions   bool     // sessions table (independent of auth)
    IncludeAuth       bool     // users table + auth handlers (implies IncludeSessions)
    AuthWithTables    bool     // generate users migration with columns
    IncludeStorage    bool     // true when StorageBackend != ""
    StorageBackend    string   // "" | "local" | "s3"
    S3StaticWatcher   bool     // include cmd/syncstatic watcher
    IncludeWS         bool
    IncludeNotify     bool
    IncludeE2E        bool
    Database          string   // "postgres"
    GoVersion         string   // "1.25"
}

// Used by `hamr add service`, not `hamr new`
type ServiceConfig struct {
    Name       string   // "billing"
    Module     string   // inherited from project's go.mod
    GoVersion  string   // inherited from project's go.mod
}
```

### 33. Generated Project Structure
```
<project>/
├── cmd/server/
│   ├── main.go                 # Bootstrap: env config, db, migrate, services, server
│   └── Dockerfile
├── cmd/syncstatic/             # (if s3-watcher) Watches static/ and syncs to S3
│   └── main.go
├── internal/
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
├── e2e-go/                        # (if e2e) Go-rod E2E tests
│   ├── main_test.go               #   TestMain: setup/teardown shared environment
│   ├── testcontainers_setup.go    #   Docker network, postgres, server containers
│   ├── helpers.go                 #   Project-specific helpers (Login, seed data, etc.)
│   ├── accounts.go                #   TestAccount definitions from seed data
│   ├── auth_test.go               #   Starter login/redirect tests
│   ├── home_test.go               #   Starter home page tests
│   ├── testdata/
│   │   └── seed_e2e.sql           #   E2E seed data (test accounts, fixtures)
│   └── README.md                  #   E2E testing guide
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
├── hamr.vendor.json            # Vendored frontend dependency versions
├── README.md
└── go.mod
```

### 34. `hamr add service <name>`

Scaffolds a new service into an existing HAMR project:
- Creates `cmd/<name>/main.go` — config, server start, graceful shutdown
- Creates `cmd/<name>/Dockerfile`
- Creates `internal/<name>/config/config.go`
- Creates `internal/<name>/handler/health.go` — health check endpoint
- Adds service to `docker/docker-compose.yaml`
- Adds Makefile targets: `run-<name>`, `build-<name>`

That's it. Doesn't touch existing code. Doesn't restructure anything. Doesn't decide where
shared types go or whether the DB is shared.

#### Auth flow when services call each other

```
Browser -> Main service (has session cookie)
  1. Main service middleware validates session via SessionManager
  2. Main service handler calls billing service:
     GET http://billing:8082/invoices/456
     Headers:
       X-Request-ID: abc-123       (propagated)
       X-Subject-ID: user-789      (from session)
  3. Billing service TrustedSubject middleware reads X-Subject-ID
  4. Billing handler calls GetSubjectID(c) -- same API as main service
```

#### What HAMR does NOT decide

These are project-level decisions, not framework decisions:
- Where shared types/DTOs live (`internal/shared/`? top-level `types/`? duplicated?)
- Whether to restructure the monolith's `internal/` when adding services
- Shared DB vs separate DB per service
- Communication direction (which service calls which)
- Whether to use an event bus or direct HTTP calls

### 35. `hamr vendor`

Downloads and vendors frontend dependencies into `static/js/`. Avoids any Node/npm
toolchain requirement for projects that don't use Tailwind.

**Usage:**
```bash
hamr vendor                    # vendors all known deps at default versions
hamr vendor --update           # re-vendors all deps at latest versions
hamr vendor htmx               # vendor only htmx
hamr vendor alpine@3.14.9      # vendor alpine at a specific version
```

**Known dependencies** (built-in registry with CDN URLs):

| Name | Default Source | Output File |
|------|---------------|-------------|
| `htmx` | `unpkg.com/htmx.org@<ver>/dist/htmx.min.js` | `static/js/htmx.min.js` |
| `alpine` | `unpkg.com/alpinejs@<ver>/dist/cdn.min.js` | `static/js/alpine.min.js` |
| `idiomorph` | `unpkg.com/idiomorph@<ver>/dist/idiomorph.min.js` | `static/js/idiomorph.min.js` |

**Behaviour:**
- Reads/writes `hamr.vendor.json` in the project root to track vendored versions
- `hamr new` calls `hamr vendor` automatically during project scaffolding
- Downloads via HTTP GET, verifies non-empty response and JS content-type
- Prints vendored file path and version for each dependency
- `--update` resolves `latest` tag from the CDN for each dependency
- Custom deps: `hamr vendor --url https://cdn.example.com/lib.min.js --out static/js/lib.min.js`
- Computes SHA256 checksum after download, stores in `hamr.vendor.json`
- On subsequent runs, verifies file matches stored checksum before skipping re-download
- `hamr vendor --verify` checks all vendored files against stored checksums without re-downloading

**`hamr.vendor.json` format:**
```json
{
  "deps": {
    "htmx": { "version": "2.0.4", "url": "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js", "out": "static/js/htmx.min.js", "sha256": "a3b1c2d4..." },
    "alpine": { "version": "3.14.9", "url": "https://unpkg.com/alpinejs@3.14.9/dist/cdn.min.js", "out": "static/js/alpine.min.js", "sha256": "e5f6a7b8..." }
  }
}
```

### 36. Key Generated Files Content

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

### 37. Generated E2E Scaffolding (if `IncludeE2E`)

All files use `//go:build e2e` build tag — excluded from normal `go test ./...`.

**`e2e-go/main_test.go`** — TestMain entry point:
- Parses `-local` flag (containerized vs local server)
- Calls `SetupSharedEnvironment()` — single DB + server for all tests
- Defers cleanup (Testcontainers Ryuk handles container teardown)

**`e2e-go/testcontainers_setup.go`** — Fully containerized infrastructure:
- Creates isolated Docker network per test run (timestamped name)
- PostgreSQL container (`postgres:18-alpine`) with network alias `postgres`
- App server container (built from project's `cmd/server/Dockerfile`)
- Waits for health check: `wait.ForHTTP("/health").WithStartupTimeout(60s)`
- Waits for migrations: polls `information_schema.tables` until expected tables exist
- Seeds test data: `//go:embed testdata/seed_e2e.sql`
- Local mode alternative: reads `E2E_DATABASE_URL` / `E2E_SERVER_URL` env vars

**`e2e-go/helpers.go`** — Project-specific test helpers:
- Imports `hamr/pkg/e2e` for generic browser/assert/htmx helpers
- `Login(t, page, email, password)` — project-specific login flow
- `CreateTestUser(t, page, ...)` — project-specific signup flow
- Any project-specific navigation helpers

**`e2e-go/accounts.go`** — Test account definitions:
- `TestAccount` struct: Email, Password, Role, Name
- Global `Accounts` map keyed by role (populated from seed SQL)
- All use password `Test1234!` (Argon2id hash in seed)

**`e2e-go/testdata/seed_e2e.sql`** — Deterministic test data:
- Uses fixed UUIDs with prefixed ranges for test isolation
- `ON CONFLICT (id) DO NOTHING` for idempotent re-runs
- Test accounts matching `accounts.go` definitions
- If auth+tables: users with hashed passwords, active sessions

**`e2e-go/auth_test.go`** — Starter tests:
- Login with valid credentials → correct redirect
- Login with invalid credentials → error message
- Login page elements exist

**`e2e-go/home_test.go`** — Starter tests:
- Home page loads
- Key elements present

**Generated Makefile targets:**
```makefile
e2e:                  ## Run E2E tests (containerized)
    go test -v -tags=e2e ./e2e-go -timeout 10m

e2e-local:            ## Run E2E tests against local server
    go test -v -tags=e2e ./e2e-go -local -timeout 10m

e2e-run:              ## Run specific E2E test: make e2e-run T=TestName
    go test -v -tags=e2e ./e2e-go -run "$(T)" -timeout 5m

e2e-run-local:        ## Run specific E2E test locally: make e2e-run-local T=TestName
    go test -v -tags=e2e ./e2e-go -local -run "$(T)" -timeout 2m
```

## Phase 7: Post-MVP

- Bearer token auth middleware (dual auth for JSON API)
- JSON-specific error handler middleware
- WebSocket PG LISTEN/NOTIFY integration (optional package)
- `hamr generate migration <name>` - generate migration pair
- `hamr dev` - dev server with hot reload (wraps air/reflex)
- SQLite support as DB option
- GoReleaser config for distribution
- Redis-backed rate limiting — MVP uses PostgreSQL UNLOGGED table; Redis option if sub-ms latency is needed
- Observability baseline (OpenTelemetry metrics, tracing, pprof) — add once apps reach production scale
- Transaction helpers / unit-of-work — projects use `sqlx.Tx` directly; abstraction adds complexity without clear benefit yet
- Migration locking for concurrent deploys — `golang-migrate` handles advisory locks; revisit if multi-instance deploys cause issues

### Migration Strategy Options

MVP runs migrations on application startup (`db.Migrate()` in `main.go`). This is
simple and works for single-instance deploys, but not all environments want this.

Planned alternatives:

- **On startup** (current default) — `db.Migrate()` called in `main.go` before server starts. Simple, works for single-instance. `golang-migrate` uses advisory locks so concurrent starts don't corrupt, but multiple instances can still race on "is migration X applied?"
- **CLI command** — `hamr migrate up`, `hamr migrate down`, `hamr migrate status`. Runs migrations explicitly as a deploy step. Better for CI/CD pipelines and multi-instance deploys. Generated project's `cmd/server/main.go` would skip auto-migration when `MIGRATE_ON_STARTUP=false`
- **Init container / job** — Kubernetes pattern: run migration as a one-shot container before the app container starts. The generated Dockerfile and Helm chart (future) would support a `migrate` entrypoint alongside the `server` entrypoint
- **Makefile targets** — `make migrate-up`, `make migrate-down`, `make migrate-create NAME=add_posts`. Wraps the CLI or calls `golang-migrate` directly

Implementation plan: add a `--migrate` flag to `hamr new` (default `startup`) with options `startup | cli | none`. When `cli`, generate `cmd/migrate/main.go` as a separate binary. When `none`, leave migration wiring to the user.

### Static Asset CDN Upload

For production deployments, serving static assets from the application server
adds load and prevents CDN caching. Projects should be able to upload all static
assets to S3/R2/CloudFront and rewrite asset URLs.

Planned approach:

- **`hamr assets upload`** — CLI command that:
  1. Walks `static/` directory
  2. Computes content hash for each file (for cache-busting filenames)
  3. Uploads to configured S3 bucket with correct `Content-Type` and `Cache-Control: public, max-age=31536000, immutable`
  4. Generates a manifest file (`static-manifest.json`) mapping original paths to CDN URLs
  5. Prints summary of uploaded/skipped files

- **Asset URL helper** — `components.AssetURL(path string) string` function that:
  - In dev mode: returns `/static/<path>` (served locally)
  - In production with manifest: returns the CDN URL from `static-manifest.json`
  - Layout template uses this helper for all `<link>` and `<script>` tags

- **Configuration**:
  ```
  ASSET_CDN_URL=https://cdn.example.com    # CDN base URL
  ASSET_BUCKET=myproject-assets             # S3 bucket name
  ASSET_UPLOAD=false                        # Enable CDN mode
  ```

- **Integration with `hamr new`**: `--asset-cdn` flag adds the manifest helper and config fields to the generated project. Without the flag, assets are served locally as today.

### `hamr add page <name>`

Scaffolds a new page (handler + templ component + route registration) into an
existing HAMR project. This is the most common scaffolding operation after
initial project creation.

**Usage:**
```bash
hamr add page dashboard                 # basic page
hamr add page admin/users               # nested under a prefix group
hamr add page settings --auth           # page requires authentication
```

**What it generates:**

| File | Content |
|------|---------|
| `internal/web/handler/<name>/handler.go` | Handler struct, `NewHandler`, `Index` method |
| `internal/web/handler/<name>/templates.templ` | Page templ component using `components.Layout` |

**What it updates:**

| File | Change |
|------|--------|
| `internal/web/server.go` | Adds import + route registration in the appropriate group |

**Behaviour:**
- Reads `go.mod` to determine module path (same as `hamr add service`)
- Infers route path from name: `dashboard` → `GET /dashboard`, `admin/users` → `GET /admin/users`
- `--auth` flag registers the route in the `authenticated` group instead of `site`
- `--method` flag for non-GET pages: `hamr add page settings --method GET,POST` generates both handler methods
- Handler follows the project's constructor injection pattern with `repo.Store` and `*slog.Logger`
- Template imports `components.Layout` and wraps content in it
- Idempotent: errors if handler package already exists

## User-Facing Documentation

Framework documentation lives in `docs/` and ships alongside the code. Each package gets
a guide written when the package is implemented (not deferred to a polish sprint).

```
docs/
├── adr/                           # Architecture decision records
│   └── 000-base-framework.md
├── getting-started/
│   ├── installation.md            # go install, prerequisites
│   ├── quickstart.md              # hamr new → running app in 5 minutes
│   └── project-structure.md       # What got generated and why
├── guides/
│   ├── configuration.md           # pkg/config — env vars, .env files
│   ├── logging.md                 # pkg/logging — structured logging, log levels
│   ├── validation.md              # pkg/validate — validators, custom rules, messages
│   ├── authentication.md          # pkg/auth — hashing, sessions, SessionStore impl
│   ├── database.md                # pkg/db — connect, migrate, embed.FS migrations
│   ├── handlers.md                # pkg/respond + pkg/htmx — content negotiation, HTMX helpers
│   ├── middleware.md              # pkg/middleware — auth, RBAC, CSRF, rate limit, etc.
│   ├── server.md                  # pkg/server — options, defaults, lifecycle hooks
│   ├── storage.md                 # pkg/storage — local, S3, signed URLs
│   ├── websockets.md              # pkg/websocket — hub, rooms, events, emitter
│   ├── background-jobs.md         # pkg/janitor — task interface, scheduling
│   ├── e2e-testing.md             # pkg/e2e — go-rod helpers, testcontainers, CI setup
│   └── vendoring.md               # hamr vendor — frontend deps, checksums
├── cli/
│   ├── hamr-new.md                # hamr new — options, flags, what gets generated
│   ├── hamr-add-service.md        # hamr add service — usage, generated files
│   └── hamr-vendor.md             # hamr vendor — usage, lock file, checksums
└── reference/
    ├── interfaces.md              # All extensibility interfaces in one place
    ├── middleware-defaults.md      # What ships enabled by default and how to override
    └── env-vars.md                # All env vars across all packages
```

**Documentation rules:**
- Each guide ships in the same sprint as its package — not deferred
- Guides are task-oriented ("How to add a custom validator") not API-reference-only
- Code examples use the generated project as context, not abstract snippets
- Reference docs are auto-maintained: interfaces table, env vars, defaults

## Key Interfaces (Extensibility Points)

| Interface         | Package          | Purpose                                                         |
|-------------------|------------------|-----------------------------------------------------------------|
| `SessionStore`    | `pkg/auth`       | Pluggable session persistence (postgres, redis, memory)         |
| `FileStorage`     | `pkg/storage`    | Pluggable file backend (local, S3, R2, GCS)                     |
| `SignableStorage` | `pkg/storage`    | Storage with URL signing                                        |
| `Task`            | `pkg/janitor`    | Custom background cleanup tasks                                 |
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
| `aws/aws-sdk-go-v2` | storage | S3 |
| `spf13/cobra` | cmd/hamr | CLI framework |
| `charmbracelet/bubbletea` | internal/cli | Interactive prompts |
| `charmbracelet/lipgloss` | internal/cli | CLI styling |
| `go-rod/rod` | e2e | Headless browser automation |
| `testcontainers/testcontainers-go` | generated e2e-go | Container orchestration for E2E tests |

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
14. `pkg/websocket`
17. `pkg/e2e`

**Sprint 6** - CLI:
18. CLI structure (cobra + bubbletea)
19. Template files for generated project
20. Generator logic
21. `hamr add service` command
22. `hamr vendor` command
23. E2E scaffolding templates
24. End-to-end test: `hamr new testproject`

**Sprint 7** - Polish:
25. Integration tests + coverage gaps (unit tests ship per sprint)
26. README, getting-started guides, and reference docs
27. CI/CD setup

## Verification

After each sprint:
- All packages compile: `go build ./...`
- All tests pass: `go test ./...`
- Linting passes: `golangci-lint run`
- Package guides written for new packages in `docs/guides/`

After Sprint 6 (CLI complete):
- Run `go run ./cmd/hamr new testproject --module github.com/test/testproject`
- Verify generated project compiles: `cd testproject && go build ./...`
- Verify generated project runs: `go run ./cmd/server`
- Verify templ generates: `templ generate`
- Verify all docs exist (adr/, features/, ai-guides/, AGENTS.md, CLAUDE.md)
- Verify Makefile targets work
- Verify docker-compose starts database
- Run `hamr add service billing` in generated project
- Verify new service compiles: `go build ./cmd/billing`
- Verify new service starts and health check responds
- Verify `TrustedSubject` middleware correctly sets subject in context
- Verify docker-compose includes the new service
- Verify Makefile has `run-billing` and `build-billing` targets
- Run `hamr vendor` — vendors htmx + alpine into `static/js/`
- Verify `hamr.vendor.json` is created with correct versions and paths
- Verify `hamr vendor htmx@2.0.4` pins a specific version
- Verify `hamr vendor --update` re-downloads at latest versions
- If e2e enabled: verify `e2e-go/` directory exists with all scaffolding files
- Verify `make e2e` runs containerized tests (requires Docker)
- Verify `//go:build e2e` isolation: `go test ./...` does NOT run e2e tests
- Verify testcontainers setup creates network, postgres, server containers
- Verify seed data loads and starter tests pass
