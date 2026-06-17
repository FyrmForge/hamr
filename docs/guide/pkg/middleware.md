# Middleware — Auth, RBAC, Flash, Rate Limiting & More

`hamr/pkg/middleware` provides a comprehensive set of Echo middleware for authentication,
authorization, flash messages, rate limiting, caching, audit logging,
CSRF, CORS, and security headers.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/middleware"
```

## Design

All middleware is group-agnostic — none hardcode path skips. The generated project wires
infrastructure middleware to the appropriate route group:

```
Global  (all routes)  → recovery, logging (scaffolded), audit
Site    (/)           → sessions, CSRF, flash, cache, secure headers
API     (/api/)       → CORS, rate limit, bearer auth
```

Auth and RBAC middleware is applied per-route for explicit, visible access control:

```
Group      → auth.Load() (populates ctx from session, the only DB call)
Per-route  → auth.RequireAuth(), auth.RequireNotAuth(), RequireRoles, RequireActive
```

## Error Pages

Per-group middleware that catches errors and renders error pages using templ components.

### Setup

```go
site.Use(middleware.ErrorPages(components.ErrorPage))
```

With per-status-code overrides:

```go
site.Use(middleware.ErrorPages(components.ErrorPage,
    middleware.Page(http.StatusNotFound, components.NotFound),
))
```

### How It Works

1. Calls the next handler
2. If no error, passes through
3. Extracts code/message from `*echo.HTTPError` (defaults to 500)
4. Skips if `c.Response().Committed`
5. Logs 5xx errors via `logging.FromContext`
6. Looks up override for the code, falls back to default page
7. Renders via `respond.HTML(c, code, page(code, message))`

### ErrorPage Function

Your project defines an `ErrorPage` function matching this signature:

```go
type ErrorPage func(code int, message string) templ.Component
```

### Middleware Ordering

Place ErrorPages after Logging so Logging sees the correct response status:

```
Logging → ErrorPages → Secure → Flash → CSRF → Auth → Handler
```

## Authentication

### Session-based auth

Create a `BrowserAuth` instance with functional options:

```go
auth := middleware.NewBrowserAuth(sm,
    middleware.WithSubjectLoader(func(ctx context.Context, id string) (any, error) {
        return repo.GetUser(ctx, id)
    }),
    middleware.WithLoginRedirect("/login"),
    middleware.WithHomeRedirect("/dashboard"),
    middleware.WithHXRedirect(), // optional: use HX-Redirect for HTMX requests, 303 for browsers
)
```

The design splits "load auth state" from "enforce auth policy":

```go
// Group-level — the only middleware that touches the DB.
// Populates ctx if logged in, clears stale cookies, never blocks.
site.Use(auth.Load())

// Per-route — pure ctx checks, zero DB calls.
site.GET("/dashboard", dashHandler.Index, auth.RequireAuth())
site.GET("/login", loginHandler.Page, auth.RequireNotAuth())
```

`WithSubjectLoader` is optional — if not set, only `SubjectIDKey` is set in context.
Projects type-assert the loaded subject in handlers:

```go
user := middleware.GetSubject(c).(*models.User)
```

### Trusted header auth (inter-service)

For services behind a gateway that forwards the authenticated subject ID:

```go
trusted := middleware.TrustedSubject()

api.GET("/billing", billingHandler.Get, trusted)
api.POST("/billing", billingHandler.Create, trusted)
```

Reads `X-Subject-ID` header and sets it in context. Same `GetSubjectID(c)` API as
session-based auth — handlers don't care how auth was resolved.

> **Security:** bare `TrustedSubject()` trusts the header with **no verification** —
> a single spoofed `X-Subject-ID` is a full auth bypass. Use it only where nothing
> untrusted can reach the mount. Otherwise gate it:
>
> ```go
> trusted := middleware.TrustedSubjectWithConfig(middleware.TrustedSubjectConfig{
>     SharedSecret:   os.Getenv("INTERNAL_SECRET"), // required in X-Internal-Secret header
>     TrustedProxies: []string{"10.0.0.0/8"},        // and/or restrict the source IP
> })
> ```
>
> When a configured gate fails, the header is ignored (subject left unset) so
> downstream RBAC fails closed. The CIDR gate relies on `c.RealIP()` — configure
> `server.WithTrustedProxies` so `X-Forwarded-For` can't be spoofed.

### Reading auth state

```go
id   := middleware.GetSubjectID(c)  // string, works with both auth modes
subj := middleware.GetSubject(c)    // any, only with session-based auth
```

## Authorization (RBAC)

RBAC middleware is applied per-route alongside auth middleware:

```go
requireAuth := auth.RequireAuth()
requireAdmin := middleware.RequireRoles(
    func(subject any, roles []string) bool {
        return slices.Contains(roles, subject.(*models.User).Role)
    },
    "admin", "superadmin",
)

adminRoutes := []echo.MiddlewareFunc{requireAuth, requireAdmin}

site.GET("/admin", adminHandler.Dashboard, adminRoutes...)
site.GET("/admin/users", adminHandler.Users, adminRoutes...)

// Active check
requireActive := middleware.RequireActive(
    func(subject any) bool {
        return subject.(*models.User).IsActive
    },
)

site.GET("/settings", settingsHandler.Index, auth.RequireAuth(), requireActive)
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
| Dynamic | everything else | `no-store, private` |

Dynamic (non-static) responses default to `no-store, private` so authenticated
pages aren't retained by the browser back-button or a shared proxy. A handler can
override per-route by setting its own `Cache-Control`; set `AllowDynamicCaching:
true` to opt the whole middleware out and serve cacheable public dynamic content.

### Custom cache config

Use `CacheControlWithConfig` to customise extension lists and durations:

```go
siteGroup.Use(middleware.CacheControlWithConfig(middleware.CacheConfig{
    ImmutableExtensions: []string{".webp", ".avif", ".png"},
    ImmutableMaxAge:     604800,  // 1 week
    StaticExtensions:    []string{".css", ".js", ".xml"},
    StaticMaxAge:        3600,    // 1 hour
}))
```

`CacheConfig` fields:

| Field | Default | Description |
|-------|---------|-------------|
| `ImmutableExtensions` | `DefaultImmutableExtensions` | File extensions cached as immutable |
| `ImmutableMaxAge` | `31536000` (1 year) | Max-age in seconds for immutable assets |
| `StaticExtensions` | `DefaultStaticExtensions` | File extensions with shorter TTL |
| `StaticMaxAge` | `86400` (1 day) | Max-age in seconds for static assets |
| `DisableCaching` | `false` | Set no-cache directives on every response |
| `AllowDynamicCaching` | `false` | Disable the `no-store, private` default on dynamic responses |

## Security Headers

```go
siteGroup.Use(middleware.Secure())
// or with custom config:
siteGroup.Use(middleware.SecureWithConfig(middleware.SecureConfig{
    ContentSecurityPolicy: "default-src 'self'; script-src 'self' 'unsafe-inline'",
    XFrameOptions:         "SAMEORIGIN",
}))
```

`SecureConfig` fields (zero-value = use default):

| Field | Default | Description |
|-------|---------|-------------|
| `ContentSecurityPolicy` | `"default-src 'self'"` | Content-Security-Policy header |
| `XFrameOptions` | `"DENY"` | X-Frame-Options header |
| `ReferrerPolicy` | `"strict-origin-when-cross-origin"` | Referrer-Policy header |
| `XSSProtection` | `"0"` | X-XSS-Protection header |
| `ContentTypeNosniff` | `"nosniff"` | X-Content-Type-Options header |

## CSRF Protection

```go
siteGroup.Use(middleware.CSRF())
// or with config:
siteGroup.Use(middleware.CSRFWithConfig(middleware.CSRFConfig{
    CookieName:  "csrf",
    TokenLookup: "form:csrf_token,header:X-CSRF-Token",
    Secure:      true,
    SameSite:    http.SameSiteStrictMode, // optional; defaults to Lax
}))
```

The CSRF cookie defaults to `SameSite=Lax` (defense-in-depth) rather than the
browser default.

## CORS

```go
apiGroup.Use(middleware.CORS())
// or with config:
apiGroup.Use(middleware.CORSWithConfig(middleware.CORSConfig{
    AllowOrigins:     []string{"https://myapp.com"},
    AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
    AllowCredentials: true,
}))
```

Default headers include `HX-Request`, `HX-Target`, `HX-Trigger`, `X-CSRF-Token`.

> **Secure default:** with no `AllowOrigins` configured, cross-origin requests are
> **denied** (no `Access-Control-Allow-Origin`) rather than allowing `*`. Pass
> explicit origins to enable CORS.

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

Values of sensitive-named query/path params (`token`, `password`, `secret`, `key`,
`code`, `otp`, `signature`, `auth`, `session`, `csrf`, …) are redacted to
`[REDACTED]` before persisting, so reset/invite tokens and API keys in URLs aren't
written to the audit sink.

Customize actor ID extraction:

```go
e.Use(middleware.AuditWithConfig(middleware.AuditConfig{
    Logger: myLogger,
    ActorIDFunc: func(c echo.Context) string {
        return c.Request().Header.Get("X-API-Key")
    },
}))
```

## Locale

Two middleware variants resolve the user's locale and inject a `Translator` into
the Echo context.

### Path-based (SEO pages)

Extracts the locale from the first URL segment (`/en/about`, `/fr/contact`) and
strips it before routing. Register with `e.Pre()`:

```go
e.Pre(middleware.LocaleFromPath(middleware.LocaleConfig{
    Bundle: bundle,
}))
```

Requests without a valid locale prefix are redirected to `/{defaultLocale}/...`
(301 for GET/HEAD, 307 for POST).

### Preference-based (dashboards)

Resolves locale from multiple sources in priority order:

```go
dashGroup.Use(middleware.LocaleFromPreference(middleware.LocaleConfig{
    Bundle: bundle,
    UserLocaleFunc: func(c echo.Context) string {
        return currentUser(c).Locale // e.g. "fr-FR"
    },
}))
```

Resolution order: **UserLocaleFunc → cookie → Accept-Language header → default**.

Region-tagged locales (e.g. `fr-FR`) are automatically resolved to the base
language (`fr`) if the exact tag isn't loaded. This applies to all sources.

### LocaleConfig

| Field | Default | Description |
|-------|---------|-------------|
| `Bundle` | required | The i18n Bundle |
| `CookieName` | `"hamr_locale"` | Cookie name for persisting locale |
| `CookieMaxAge` | 1 year | Cookie max-age in seconds |
| `CookieSecure` | `false` | Secure flag on locale cookie |
| `DefaultLocale` | from bundle | Fallback locale |
| `UserLocaleFunc` | nil | Optional function returning user's preferred locale |

### Reading locale in handlers

```go
locale    := middleware.GetLocale(c)    // "fr"
direction := middleware.GetDirection(c) // "ltr" or "rtl"
```

Or use the i18n package directly:

```go
tr := i18n.FromContext(c)
tr.T("home.title")
```

## API Reference

```go
// Error Pages
type ErrorPage func(code int, message string) templ.Component
type PageOverride struct { Code int; Page ErrorPage }
func Page(code int, page ErrorPage) PageOverride
func ErrorPages(defaultPage ErrorPage, overrides ...PageOverride) echo.MiddlewareFunc

// Locale
type LocaleConfig struct { ... }
func LocaleFromPath(cfg LocaleConfig) echo.MiddlewareFunc
func LocaleFromPreference(cfg LocaleConfig) echo.MiddlewareFunc
func GetLocale(c echo.Context) string
func GetDirection(c echo.Context) string

// Auth
type SubjectLoader func(ctx context.Context, subjectID string) (any, error)
type BrowserAuthOption func(*BrowserAuth)
func NewBrowserAuth(sm *auth.SessionManager, opts ...BrowserAuthOption) *BrowserAuth
func WithSubjectLoader(loader SubjectLoader) BrowserAuthOption
func WithLoginRedirect(url string) BrowserAuthOption
func WithHomeRedirect(url string) BrowserAuthOption
func WithHXRedirect() BrowserAuthOption
func (b *BrowserAuth) Load() echo.MiddlewareFunc
func (b *BrowserAuth) RequireAuth() echo.MiddlewareFunc
func (b *BrowserAuth) RequireNotAuth() echo.MiddlewareFunc
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
func NewMemoryStore(opts ...MemoryStoreOption) *MemoryStore
func NewPGStore(db DB) *PGStore

// Cache
func CacheControl(disableCaching bool) echo.MiddlewareFunc
func CacheControlWithConfig(cfg CacheConfig) echo.MiddlewareFunc

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
