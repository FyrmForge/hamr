# Routing and handlers

HAMR's server package wraps Echo with production defaults — panic recovery, timeouts, gzip, cache and security headers, graceful shutdown — and the scaffold registers every route in one file, one Go package per page.

**Package references:** [Server](pkg/server.md), [Respond](pkg/respond.md), [Middleware](pkg/middleware.md), [Ctx](pkg/ctx.md)

## Server setup

Create a server with functional options. Trimmed from the scaffold:

```go title="cmd/site/main.go"
import "github.com/FyrmForge/hamr/pkg/server"

srv, err := server.New(
    server.WithPort(envPort),
    server.WithDevMode(envDevMode),
    server.WithStaticDir("frontend/static"),
    server.WithStaticDistDir("frontend/dist"),
    server.WithGeneratedDir("generated"),
    server.WithTrustedProxies(envTrustedProxies...), // TRUSTED_PROXIES, comma-separated
)
if err != nil {
    log.Error("failed to create server", "error", err)
    os.Exit(1)
}

web.RegisterStaticPages(srv)
// ... --generate, database, sessions ...
api.RegisterRoutes(srv, &api.Deps{Store: store})
web.RegisterRoutes(srv, &web.Deps{ /* ... */ })

if err := srv.Start(); err != nil {
    log.Error("server stopped", "error", err)
    os.Exit(1)
}
```

`Start` blocks until SIGINT/SIGTERM, then performs graceful shutdown.

`WithTrustedProxies` lists the CIDRs of load balancers allowed to set `X-Forwarded-For` / `X-Forwarded-Proto`. Leave it empty when nothing sits in front of the app: forwarded headers are then ignored, so a client can't spoof its IP (which is also the default rate-limit key). The keyword `"cloudflare"` trusts Cloudflare's edge ranges, refreshed every 24h.

### Production defaults

Applied by `server.New` to every route:
- Panic recovery
- Max request body: 2MB (`WithMaxBodySize`)
- Request timeout: 30s (`WithTimeout`)
- `Cache-Control`: immutable for fingerprinted `/static/` files, `max-age` for other static assets, `no-store, private` for dynamic responses; nothing cached in dev mode
- Gzip (skipped for WebSocket upgrades; tune with `WithGzipConfig`)
- HEAD requests are answered by the matching GET handler with no body; an app's own HEAD route takes precedence, and handlers see `Method == HEAD`
- Security headers, outside dev mode only: `Content-Security-Policy: default-src 'self'`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-Content-Type-Options: nosniff`, `X-XSS-Protection: 0`

## Route groups

`internal/web/server.go` puts every page on one site group. Trimmed:

```go title="internal/web/server.go"
import (
    hamrmw "github.com/FyrmForge/hamr/pkg/middleware"

    "github.com/user/myapp/internal/middleware" // project-local
    "github.com/user/myapp/internal/web/components"
)

func RegisterRoutes(srv *server.Server, deps *Deps) {
    e := srv.Echo()

    csp := "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'"

    site := e.Group("")
    site.Use(middleware.Logging())
    site.Use(hamrmw.ErrorPages(components.ErrorPage))
    site.Use(hamrmw.SecureWithConfig(hamrmw.SecureConfig{ContentSecurityPolicy: csp}))
    site.Use(hamrmw.FlashWithConfig(hamrmw.FlashConfig{Secure: !deps.DevMode}))
    site.Use(hamrmw.CSRFWithConfig(hamrmw.CSRFConfig{Secure: !deps.DevMode}))

    // ... auth loader + routes, see below
}
```

- `middleware.Logging()` is **project code** in `internal/middleware/logging.go` (request ID + request log), not a hamr package. It is added per group, not globally.
- The CSP allows inline scripts because the layout configures htmx inline (see [Templates and htmx](templates.md#htmx-setup-in-the-layout)).
- `hamrmw.Flash()` and `hamrmw.CSRF()` set `Secure` cookies, which browsers won't store over plain HTTP (e.g. opening the dev server via a LAN IP). The `WithConfig` forms with `Secure: !deps.DevMode` keep local dev working.

The API group lives in `internal/api/server.go`, with its own `Deps` and `RegisterRoutes`:

```go title="internal/api/server.go"
api := srv.Echo().Group("/api")
api.Use(middleware.Logging())
api.GET("/health", healthHandler.Health)
```

Add rate limiting or bearer auth there, e.g. `api.Use(hamrmw.RateLimit(hamrmw.NewMemoryStore()))`.

CORS is global and only installed when `BASE_URL` is set (`cmd/site/main.go`). `hamrmw.CORS()` with no origins **denies** every cross-origin request, so always list them:

```go
srv.Echo().Use(hamrmw.CORSWithConfig(hamrmw.CORSConfig{
    AllowOrigins:     []string{baseOrigin},
    AllowCredentials: true,
}))
```

### Auth middleware

`hamrmw.NewBrowserAuth` returns a `*BrowserAuth`. Its `Load()` runs once on the group (the only DB call); `RequireAuth()` and `RequireNotAuth()` are context-only checks applied per route, so each route's access rule is visible where it's defined:

```go title="internal/web/server.go"
auth := hamrmw.NewBrowserAuth(deps.SessionManager,
    hamrmw.WithSubjectLoader(func(reqCtx context.Context, id string) (any, error) {
        u, err := deps.Store.Users().GetByID(reqCtx, id)
        if u == nil {
            return nil, err // untyped nil, see Authentication guide
        }
        return u, err
    }),
    hamrmw.WithLoginRedirect("/login"),
    hamrmw.WithHomeRedirect("/"),
)
site.Use(auth.Load())

site.GET("/dashboard", dashHandler.Index, auth.RequireAuth())
site.POST("/logout", loginHandler.Logout, auth.RequireAuth())
```

The variable `auth` shadows the `github.com/FyrmForge/hamr/pkg/auth` import for the rest of `RegisterRoutes`; rename it if you need the package below that point. See [Authentication](authentication.md).

## Handler package layout

HAMR scaffolds use **one Go package per page**, with the directory tree mirroring the URL path. A page package owns its handler, its templates, and any HTMX helper routes the page needs (validation endpoints, partials, modals).

```text
URL                     Package path
/                       internal/web/handler/home/
/login                  internal/web/handler/auth/login/
/register               internal/web/handler/auth/register/
/admin                  internal/web/handler/admin/
/admin/users            internal/web/handler/admin/user/
/admin/users/:id/edit   internal/web/handler/admin/user/edit/
```

Conventions:

- **URL plural, package singular.** `/users` → package `user`. Go package names are short, lowercase, single-word.
- **Parent directories without a page have no `handler.go`.** If `/admin` itself isn't a page but `/admin/users` is, `internal/web/handler/admin/` is just a directory containing the `user/` sub-package.
- **Pure-action endpoints (no view) live with the most related page.** `POST /logout` lives in `handler/auth/login/` because it's the inverse of login, not a page in its own right.
- **Section-level shared code lives outside the handler tree.** Helpers used by multiple page packages in a section (e.g. session-cookie helpers used by both login and register) go in `internal/auth/` (or similar). Keep `handler/` as a flat list of page packages.

### Page package anatomy

```text
internal/web/handler/auth/login/
    handler.go           // package login: NewHandler, Page, Submit, Logout, FormRules
    login.templ          // package login: loginPage, loginForm
    login_templ.go       // generated by templ
```

```go title="internal/web/handler/auth/login/handler.go"
package login

type handler struct {
    authService    *service.AuthService
    sessionManager *hamrauth.SessionManager
    FormRules      validate.Form
}

func NewHandler(...) *handler { ... }
func (h *handler) Page(c echo.Context) error    { ... }  // GET /login
func (h *handler) Submit(c echo.Context) error  { ... }  // POST /login
func (h *handler) Logout(c echo.Context) error  { ... }  // POST /logout
```

### Wiring

Routes are registered centrally in `internal/web/server.go`. Each page package contributes its page route plus every HTMX helper route it owns:

```go title="internal/web/server.go"
loginHandler := login.NewHandler(deps.AuthService, deps.SessionManager)
site.GET("/login", loginHandler.Page, auth.RequireNotAuth())
site.POST("/login", loginHandler.Submit, auth.RequireNotAuth())
site.POST("/login/validate/:field", loginHandler.FormRules.ValidationHandler("field"), auth.RequireNotAuth())
site.POST("/logout", loginHandler.Logout, auth.RequireAuth())
```

To add a page: create the package, add any new dependency to `web.Deps`, set it in `cmd/site/main.go`, and register the routes in `RegisterRoutes`. Central wiring keeps every route's path and middleware visible in one file.

### Static pages

Pages that need no database or session state register in `RegisterStaticPages` instead:

```go title="internal/web/server.go"
func RegisterStaticPages(srv *server.Server) {
    h := about.NewHandler()
    srv.StaticPage("/about", h.About)
}
```

`srv.StaticPage` records the page for `./bin/site --generate` (which renders it to `generated/about/index.html` and exits) and registers a `GET` route that serves the pre-rendered file, falling back to the handler. The route is on the root Echo instance, so none of the site group's middleware (CSRF, flash, auth, error pages, the site CSP) runs for it. Outside dev mode it gets only the server-wide `default-src 'self'` CSP, which blocks the layout's inline htmx setup script; keep static pages free of HTMX behaviour or relax the CSP globally.

## Writing handlers

Handlers are methods on an unexported struct that holds dependencies:

```go
type handler struct {
    users *service.UserService
}

func NewHandler(users *service.UserService) *handler {
    return &handler{users: users}
}
```

A handler holds services, never a `repo.Store`. Data access and the logic around
it live in `internal/service/` — see [Database](database.md#repository-pattern).

Use `logging.FromContext(c.Request().Context())` for request-scoped logging instead of storing a logger on the struct.

### HTML responses

Render a templ component from the same package:

```go
// GET /
func (h *handler) Index(c echo.Context) error {
    return respond.HTML(c, http.StatusOK, homePage(c))
}
```

### Redirects

`respond.Redirect` is HTMX-aware: for HTMX requests it sets `HX-Redirect` and returns 200; otherwise it sends a 303.

```go
return respond.Redirect(c, "/dashboard")
```

### JSON responses

JSON endpoints belong on the `/api` group. Repositories return `nil, nil` when a row doesn't exist, so check for a nil result as well as an error:

```go
// GET /api/users/:id
func (h *handler) Get(c echo.Context) error {
    user, err := h.users.Get(c.Request().Context(), c.Param("id"))
    if err != nil {
        return err // 500
    }
    if user == nil {
        return echo.NewHTTPError(http.StatusNotFound, "user not found")
    }
    return respond.JSON(c, http.StatusOK, user)
}
```

### Error responses

Return an `echo.HTTPError` from handlers:

```go
return echo.NewHTTPError(http.StatusForbidden, "Access denied")
return echo.NewHTTPError(http.StatusNotFound, "Not found")
```

What the client gets depends on the group. `hamrmw.ErrorPages` is installed per group; the site group has it, so an `HTTPError` there renders `components.ErrorPage` (`internal/web/components/error.templ`) as HTML with that status code. Any other error renders as a 500, and 5xx errors are logged. Override specific codes with `hamrmw.Page`:

```go
site.Use(hamrmw.ErrorPages(components.ErrorPage,
    hamrmw.Page(http.StatusNotFound, components.NotFoundPage), // your own func(code int, msg string) templ.Component
))
```

Groups without `ErrorPages`, like the scaffold's `/api` group, fall through to Echo's default handler and return JSON (`{"message": "..."}`). Because the site group is mounted at `""`, unmatched paths outside `/api` also get the HTML 404 page.

## Context helpers

Use type-safe context keys instead of string lookups:

```go
import "github.com/FyrmForge/hamr/pkg/ctx"

// Get the string ID (works with session-based and trusted-header auth)
userID, ok := ctx.Get(c, ctx.SubjectIDKey)

// Get the fully loaded subject (only set when a SubjectLoader is configured)
user, _ := hamrmw.GetSubject(c).(*repo.User)

// Create custom keys for your project
var TenantKey = ctx.NewKey[string]("tenant_id")
ctx.Set(c, TenantKey, tenantID)
```

The scaffold wraps the subject lookup as `components.GetUser(c)` (`internal/web/components/helpers.go`), which returns `*repo.User` or nil.

## Pagination

```go
func (h *handler) List(c echo.Context) error {
    page, size := respond.ParsePagination(c, 20)

    users, total, err := h.users.List(c.Request().Context(), page, size)
    if err != nil {
        return echo.NewHTTPError(http.StatusInternalServerError, "Failed to list users")
    }

    return respond.JSON(c, http.StatusOK, respond.PagedResponse[repo.User]{
        Data: users,
        Page: respond.NewPage(page, size, total),
    })
}
```

`ParsePagination` reads `page` and `size` query params, defaulting page to 1 and clamping size to [1, 100].

## Escape hatch

Access the underlying Echo instance for anything not covered by the wrapper:

```go
e := srv.Echo()
e.Validator = myValidator
e.IPExtractor = echo.ExtractIPFromXFFHeader()
```

Overwriting `e.IPExtractor` (or `e.SchemeExtractor`) discards the
`WithTrustedProxies` config, including the Cloudflare refresh.
