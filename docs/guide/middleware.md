# Middleware — Auth, RBAC, Flash, Rate Limiting & More

`hamr/pkg/middleware` provides a comprehensive set of Echo middleware for authentication,
authorization, flash messages, rate limiting, request tracing, caching, audit logging,
CSRF, CORS, and security headers.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/middleware"
```

## Design

All middleware is group-agnostic — none hardcode path skips. The generated project wires
each middleware to the appropriate route group:

```
Global  (all routes)  → recovery, request ID, logging, audit
Site    (/)           → sessions, CSRF, flash, cache, secure headers
API     (/api/)       → CORS, rate limit, bearer auth
```

## Authentication

### Session-based auth

```go
cfg := middleware.AuthConfig{
    SessionManager: sm,
    SubjectLoader:  func(ctx context.Context, id string) (any, error) {
        return repo.GetUser(ctx, id)
    },
    LoginRedirect: "/login",
    HomeRedirect:  "/dashboard",
}
```

Four middleware variants:

```go
// Require valid session — returns 401 on failure
siteGroup.Use(middleware.Auth(cfg))

// Require valid session — redirects to login on failure
siteGroup.Use(middleware.RequireAuth(cfg))

// Populate context if logged in, never block
siteGroup.Use(middleware.OptionalAuth(cfg))

// Redirect already-authenticated users to home
loginGroup.Use(middleware.RequireNotAuth(cfg))
```

`SubjectLoader` is optional — if nil, only `SubjectIDKey` is set in context. Projects
type-assert the loaded subject in handlers:

```go
user := middleware.GetSubject(c).(*models.User)
```

### Trusted header auth (inter-service)

For services behind a gateway that forwards the authenticated subject ID:

```go
internalGroup.Use(middleware.TrustedSubject())
```

Reads `X-Subject-ID` header and sets it in context. Same `GetSubjectID(c)` API as
session-based auth — handlers don't care how auth was resolved.

### Reading auth state

```go
id   := middleware.GetSubjectID(c)  // string, works with both auth modes
subj := middleware.GetSubject(c)    // any, only with session-based auth
```

## Authorization (RBAC)

```go
adminGroup.Use(middleware.RequireRoles(
    func(subject any, roles []string) bool {
        user := subject.(*models.User)
        return slices.Contains(roles, user.Role)
    },
    "admin", "superadmin",
))

activeGroup.Use(middleware.RequireActive(
    func(subject any) bool {
        return subject.(*models.User).IsActive
    },
))
```

Returns 401 if no subject, 403 if the check fails.

## Flash Messages

One-time messages stored in a cookie and shown on the next request.

### Setup

```go
siteGroup.Use(middleware.Flash())
// or with config:
siteGroup.Use(middleware.FlashWithConfig(middleware.FlashConfig{
    Path:   "/",
    Secure: true,
}))
```

### Setting a flash

```go
middleware.SetFlash(c, "Account created successfully", middleware.FlashSuccess)
```

Flash types: `FlashInfo`, `FlashSuccess`, `FlashWarning`, `FlashError`.

### Reading a flash

```go
if flash := middleware.GetFlash(c); flash != nil {
    fmt.Printf("[%s] %s\n", flash.Type, flash.Message)
}
```

The cookie is cleared after reading — flash messages are shown exactly once.

## Rate Limiting

### In-memory store (dev/testing)

```go
store := middleware.NewMemoryStore()
apiGroup.Use(middleware.RateLimit(store))
```

### PostgreSQL store (production)

```go
pgStore := middleware.NewPGStore(database)
pgStore.CreateTable(ctx) // creates UNLOGGED _rate_limits table

apiGroup.Use(middleware.RateLimitWithConfig(middleware.RateLimitConfig{
    Store:  pgStore,
    Rate:   100,
    Burst:  20,
    Window: time.Minute,
    KeyFunc: func(c echo.Context) (string, error) {
        return c.RealIP(), nil
    },
}))
```

Defaults: 60 req/min + 10 burst. Fails open on store errors. Sets response headers:
`X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After`.

### Cleanup expired entries

Wire the PG store cleanup into the janitor:

```go
pgStore.Cleanup(ctx, time.Minute) // removes expired windows
```

## Request ID

Generates or propagates a UUID request ID, attaches a structured logger to the context,
and logs request completion.

```go
e.Use(middleware.RequestID())
```

Uses `X-Request-ID` from the incoming request or generates a new UUID v4. Logs method,
path, status, duration, client IP, and request ID. Excludes `/static` paths from
logging.

## Cache Control

```go
siteGroup.Use(middleware.CacheControl(false))
// or disable caching entirely:
siteGroup.Use(middleware.CacheControl(true))
```

| Asset type | Extensions | Cache-Control |
|-----------|------------|---------------|
| Immutable | .webp, .jpg, .png, .gif, .svg, .ico, .woff2, .ttf, ... | `public, max-age=31536000, immutable` |
| Static | .css, .js | `public, max-age=86400` |
| Other | everything else | (no header set) |

## Security Headers

```go
siteGroup.Use(middleware.Secure())
// or with custom CSP:
siteGroup.Use(middleware.SecureWithConfig(middleware.SecureConfig{
    ContentSecurityPolicy: "default-src 'self'; script-src 'self' 'unsafe-inline'",
}))
```

Default headers: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
`Referrer-Policy: strict-origin-when-cross-origin`, `X-XSS-Protection: 0`,
`Content-Security-Policy: default-src 'self'`.

## CSRF Protection

```go
siteGroup.Use(middleware.CSRF())
// or with config:
siteGroup.Use(middleware.CSRFWithConfig(middleware.CSRFConfig{
    CookieName:  "csrf",
    TokenLookup: "form:csrf_token,header:X-CSRF-Token",
    Secure:      true,
}))
```

## CORS

```go
apiGroup.Use(middleware.CORS())
// or with config:
apiGroup.Use(middleware.CORSWithConfig(middleware.CORSConfig{
    AllowOrigins: []string{"https://myapp.com"},
    AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowHeaders: []string{"Origin", "Content-Type", "Authorization"},
}))
```

Default headers include `HX-Request`, `HX-Target`, `HX-Trigger`, `X-CSRF-Token`.

## Audit Logging

Log non-GET mutations (POST, PUT, DELETE, PATCH):

```go
e.Use(middleware.Audit(myAuditLogger))
```

Implement the `AuditLogger` interface:

```go
type AuditLogger interface {
    Log(ctx context.Context, entry *AuditEntry) error
}
```

`AuditEntry` contains: `ActorID`, `Action` (HTTP method), `EntityType` (route path),
`Data` (method, path, status, query, path params), `Timestamp`.

Customize actor ID extraction:

```go
e.Use(middleware.AuditWithConfig(middleware.AuditConfig{
    Logger: myLogger,
    ActorIDFunc: func(c echo.Context) string {
        return c.Request().Header.Get("X-API-Key")
    },
}))
```

## API Reference

```go
// Auth
type SubjectLoader func(ctx context.Context, subjectID string) (any, error)
type AuthConfig struct { ... }
func Auth(cfg AuthConfig) echo.MiddlewareFunc
func RequireAuth(cfg AuthConfig) echo.MiddlewareFunc
func OptionalAuth(cfg AuthConfig) echo.MiddlewareFunc
func RequireNotAuth(cfg AuthConfig) echo.MiddlewareFunc
func GetSubjectID(c echo.Context) string
func GetSubject(c echo.Context) any

// Trusted
func TrustedSubject() echo.MiddlewareFunc

// RBAC
type RoleChecker func(subject any, roles []string) bool
type ActiveChecker func(subject any) bool
func RequireRoles(checker RoleChecker, roles ...string) echo.MiddlewareFunc
func RequireActive(checker ActiveChecker) echo.MiddlewareFunc

// Flash
func Flash() echo.MiddlewareFunc
func FlashWithConfig(cfg FlashConfig) echo.MiddlewareFunc
func SetFlash(c echo.Context, message string, flashType FlashType)
func GetFlash(c echo.Context) *FlashMessage

// Rate limiting
func RateLimit(store RateLimitStore) echo.MiddlewareFunc
func RateLimitWithConfig(cfg RateLimitConfig) echo.MiddlewareFunc
func NewMemoryStore() *MemoryStore
func NewPGStore(db DB) *PGStore

// Request ID
func RequestID() echo.MiddlewareFunc

// Cache
func CacheControl(disableCaching bool) echo.MiddlewareFunc

// Security
func Secure() echo.MiddlewareFunc
func SecureWithConfig(cfg SecureConfig) echo.MiddlewareFunc

// CSRF
func CSRF() echo.MiddlewareFunc
func CSRFWithConfig(cfg CSRFConfig) echo.MiddlewareFunc

// CORS
func CORS() echo.MiddlewareFunc
func CORSWithConfig(cfg CORSConfig) echo.MiddlewareFunc

// Audit
func Audit(logger AuditLogger) echo.MiddlewareFunc
func AuditWithConfig(cfg AuditConfig) echo.MiddlewareFunc
```
