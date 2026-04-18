# Handlers & Routing

HAMR's server package wraps Echo with opinionated defaults — panic recovery, graceful shutdown, security headers — so you can focus on handlers rather than boilerplate. This guide covers server setup, route groups, writing handlers, middleware wiring, and error handling.

**Package references:** [Server](pkg/server.md), [Respond](pkg/respond.md), [Middleware](pkg/middleware.md), [Ctx](pkg/ctx.md)

---

## Server Setup

Create a server with functional options:

```go
import "github.com/FyrmForge/hamr/pkg/server"

srv, err := server.New(
    server.WithPort(envPort),
    server.WithDevMode(envDevMode),
    server.WithTimeout(30*time.Second),
    server.WithStaticDir("static"),
)
if err != nil {
    log.Fatal(err)
}

// Register routes...

if err := srv.Start(); err != nil {
    log.Fatal(err)
}
```

`Start` blocks until SIGINT/SIGTERM, then performs graceful shutdown.

### Production Defaults

Enabled automatically:
- Panic recovery
- Request timeout: 30s
- Max request body: 2MB
- Security headers (disabled in dev mode): `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Content-Security-Policy`

---

## Route Groups

Organize routes into groups with shared middleware:

```go
// Global middleware (all routes)
srv.Echo().Use(middleware.Logging())

// Site routes — sessions, CSRF, flash
site := srv.Group("", middleware.Flash(), middleware.CSRF())
site.GET("/", homeHandler.Home)
site.GET("/login", authHandler.LoginForm)
site.POST("/login", authHandler.Login)

// API routes — CORS, rate limiting
api := srv.Group("/api", middleware.CORS(), middleware.RateLimit(store))
api.GET("/users", userHandler.List)
api.POST("/users", userHandler.Create)
```

### Typical Middleware Layout

Infrastructure middleware is applied at the group level:

```
Global  (all routes)  → recovery, logging (scaffolded), audit
Site    (/)           → sessions, CSRF, flash, cache, secure headers
API     (/api/)       → CORS, rate limit, bearer auth
```

The auth loader runs on the group (one DB call), while policy and RBAC checks are applied per-route so every route's access requirements are visible at its definition site:

```go
// Auth loader on the group, policy checks per-route
site.Use(auth.Load())
site.GET("/dashboard", dashHandler.Index, auth.RequireAuth())
site.POST("/logout", authHandler.Logout, auth.RequireAuth())
```

---

## Writing Handlers

Handlers are methods on a struct that holds dependencies:

```go
type Handler struct {
    repo *UserRepo
}

func NewHandler(repo *UserRepo) *Handler {
    return &Handler{repo: repo}
}
```

Use `logging.FromContext(c.Request().Context())` for request-scoped logging instead of storing a logger on the struct.

### HTML Response

Render a templ component:

```go
func (h *Handler) Home(c echo.Context) error {
    return respond.HTML(c, http.StatusOK, templates.HomePage())
}
```

### JSON Response

```go
func (h *Handler) GetUser(c echo.Context) error {
    id := c.Param("id")
    user, err := h.repo.GetByID(c.Request().Context(), id)
    if err != nil {
        return echo.NewHTTPError(http.StatusNotFound, "User not found")
    }
    return respond.JSON(c, http.StatusOK, user)
}
```

### Error Responses

Return an `echo.HTTPError` from handlers — the `ErrorPages` middleware catches it and
renders the appropriate error page:

```go
return echo.NewHTTPError(http.StatusForbidden, "Access denied")
return echo.NewHTTPError(http.StatusNotFound, "Not found")
```

The `ErrorPages` middleware renders error pages using templ components registered at
the group level (see [Middleware](pkg/middleware.md)).

---

## Context Helpers

Use type-safe context keys instead of string lookups:

```go
import "github.com/FyrmForge/hamr/pkg/ctx"

// Get the string ID (works with session-based and trusted-header auth)
userID, ok := ctx.Get(c, ctx.SubjectIDKey)

// Get the fully loaded model (only available when SubjectLoader is configured)
if subject := middleware.GetSubject(c); subject != nil {
    user := subject.(*models.User)
}

// Create custom keys for your project
var TenantKey = ctx.NewKey[string]("tenant_id")
ctx.Set(c, TenantKey, tenantID)
```

---

## Pagination

```go
func (h *Handler) ListUsers(c echo.Context) error {
    page, size := respond.ParsePagination(c, 20)

    users, total, err := h.repo.List(c.Request().Context(), page, size)
    if err != nil {
        return echo.NewHTTPError(http.StatusInternalServerError, "Failed to list users")
    }

    return respond.JSON(c, http.StatusOK, respond.PagedResponse[User]{
        Data: users,
        Page: respond.NewPage(page, size, total),
    })
}
```

`ParsePagination` reads `page` and `size` query params, defaulting page to 1 and clamping size to [1, 100].

---

## Escape Hatch

Access the underlying Echo instance for anything not covered by the wrapper:

```go
e := srv.Echo()
e.Validator = myValidator
e.IPExtractor = echo.ExtractIPFromXFFHeader()
```

---

## Next Steps

- [Templates & Frontend](05-templates-frontend.md) — Templ components, HTMX, optional Alpine.js
- [Forms & Validation](06-forms-validation.md) — Form handling and validation
- [Authentication](07-authentication.md) — Sessions and auth middleware
