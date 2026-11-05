# Authentication

HAMR uses server-side sessions (not JWTs), so a session can be revoked instantly, and Argon2id for password hashing. Every scaffolded project ships working `/login`, `/register` and `/logout` routes built from these pieces.

**Package references:** [Auth](pkg/auth.md), [Middleware](pkg/middleware.md) (auth sections)

## Password hashing

Argon2id with production-safe defaults (3 iterations, 64 MB memory, 2 threads):

```go
import "github.com/FyrmForge/hamr/pkg/auth"

// Hash a password (returns PHC-format string)
hash, err := auth.HashPassword("s3cret!")

// Verify a password
match, err := auth.CheckPassword("s3cret!", hash)
if !match {
    // wrong password
}
```

## Session management

### The SessionStore interface

Projects implement this for their database; the scaffold's `repo.Store` embeds it:

```go
type SessionStore interface {
    Create(ctx context.Context, s *Session) error
    GetByToken(ctx context.Context, token string) (*Session, error)
    Delete(ctx context.Context, id string) error
    DeleteBySubjectID(ctx context.Context, subjectID string) error
}
```

### Creating a SessionManager

```go
sm := auth.NewSessionManager(store,
    auth.WithDuration(24*time.Hour),
    auth.WithCookieName("session"),
    auth.WithCookieSecure(true),
    auth.WithSameSite(http.SameSiteLaxMode),
)
```

Defaults: 7-day duration, cookie name `session_token`, Secure true, SameSite Lax.

### Session operations

```go
type SessionMeta struct {
    IP        string `json:"ip"`
    UserAgent string `json:"ua"`
}

// Create a session after login — metadata can be any JSON-serializable type
session, err := sm.CreateSession(ctx, userID, SessionMeta{IP: clientIP, UserAgent: ua})

// Validate a session (deletes expired sessions automatically)
session, err := sm.ValidateSession(ctx, token)

// Read metadata back into your struct
meta, err := auth.SessionMetadata[SessionMeta](session)

// Delete on logout
err := sm.DeleteSession(ctx, sessionID)

// Delete all sessions for a user (password change, account compromise)
err := sm.DeleteSubjectSessions(ctx, userID)
```

## Auth middleware

Create a `BrowserAuth` instance with your session manager and options (`hamrmw` is the scaffold's alias for `pkg/middleware`):

```go title="internal/web/server.go"
auth := hamrmw.NewBrowserAuth(deps.SessionManager,
    hamrmw.WithSubjectLoader(func(reqCtx context.Context, id string) (any, error) {
        u, err := deps.Store.Users().GetByID(reqCtx, id)
        if u == nil {
            return nil, err // untyped nil, see below
        }
        return u, err
    }),
    hamrmw.WithLoginRedirect("/login"),
    hamrmw.WithHomeRedirect("/"), // where RequireNotAuth sends logged-in users
)
```

The subject loader is route wiring, not a handler, so it reaches the store
directly — this is the one place in `server.go` that does. Handlers themselves
call services.

Return an untyped `nil` when the subject no longer exists. A nil `*repo.User`
returned as `any` is not `== nil`, so `Load()` would skip clearing the stale
session and role checks would receive a nil pointer.

### Load, then enforce per route

The design separates "load auth state" (one DB call) from "enforce policy" (pure ctx checks). Auth and RBAC middleware is applied per-route so every route's access requirements are visible at its definition site. Infrastructure middleware (sessions, CSRF, flash, secure headers) stays on the group.

```go title="internal/web/server.go"
// Group-level — the only middleware that touches the DB.
// Populates ctx if logged in, clears stale cookies, never blocks.
site.Use(auth.Load())

// Per-route — pure ctx checks, zero DB calls.
site.GET("/dashboard", dashHandler.Index, auth.RequireAuth())
site.GET("/login", loginHandler.Page, auth.RequireNotAuth())
```

### Reading auth state in handlers

```go
// Get the subject ID (string, works with both auth modes; "" when anonymous)
userID := middleware.GetSubjectID(c)

// Get the loaded subject (requires a SubjectLoader); ok is false when anonymous
user, ok := middleware.GetSubject(c).(*repo.User)
```

In templates, the scaffold's `components.GetUser(c)` wraps the same lookup and returns `*repo.User` or nil.

## Login and register flows

The scaffold's `internal/web/handler/auth/login` and `.../register` packages follow the [form handling pattern](validation.md#form-handling-pattern): bind, validate with `validate.Form`, call `AuthService`, create a session, set the cookie, redirect. Trimmed:

```go title="internal/web/handler/auth/login/handler.go"
// POST /login
func (h *handler) Submit(c echo.Context) error {
    var f LoginForm
    if err := c.Bind(&f); err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, "invalid form data")
    }
    f.Email = strings.ToLower(f.Email)

    if errs := h.FormRules.Validate(c); errs != nil {
        return respond.HTML(c, http.StatusUnprocessableEntity, loginForm(c, f, errs))
    }

    user, err := h.authService.Authenticate(c.Request().Context(), f.Email, f.Password)
    if err != nil {
        return respond.HTML(c, http.StatusUnauthorized, loginForm(c, f, map[string]string{
            "general": "Invalid email or password",
        }))
    }

    session, err := h.sessionManager.CreateSession(c.Request().Context(), user.ID, nil)
    if err != nil {
        return echo.NewHTTPError(http.StatusInternalServerError, "session error")
    }

    auth.SetSession(c, h.sessionManager, session) // internal/auth/cookies.go
    return respond.Redirect(c, "/")
}
```

`auth.SetSession` (project code in `internal/auth/cookies.go`, shared by login and register) writes an `HttpOnly` cookie from the session manager's name, path, domain, `Secure` and `SameSite` settings. `Logout` validates the cookie, deletes the session, clears the cookie and redirects to `/login`.

Register is the same shape: `h.authService.Register(ctx, email, password, name)` hashes the password and persists the user — hashing belongs to the service, not the handler — then the handler creates a session, sets a flash and redirects. A duplicate email comes back as `service.ErrEmailTaken`, rendered as a field error with `422`.

## Authorization (RBAC)

After authentication, restrict access by role. Define middleware slices and pass them per-route:

```go
requireAdmin := hamrmw.RequireRoles(
    func(subject any, roles []string) bool {
        return slices.Contains(roles, subject.(*repo.User).Role)
    },
    "admin", "superadmin",
)

adminRoutes := []echo.MiddlewareFunc{auth.RequireAuth(), requireAdmin}

site.GET("/admin", adminHandler.Dashboard, adminRoutes...)
site.GET("/admin/users", adminHandler.Users, adminRoutes...)
```

Check active status:

```go
requireActive := hamrmw.RequireActive(
    func(subject any) bool {
        return subject.(*repo.User).Active
    },
)

site.GET("/settings", settingsHandler.Index, auth.RequireAuth(), requireActive)
```

## Trusted header auth (inter-service)

When your app runs behind an API gateway (e.g., nginx, Traefik) that has already authenticated the request and forwards the subject ID in `X-Subject-ID`, gate the header so only the gateway can set it:

```go
trusted := hamrmw.TrustedSubjectWithConfig(hamrmw.TrustedSubjectConfig{
    SharedSecret:   config.GetEnvOrPanic("INTERNAL_SECRET"), // sent by the gateway
    SecretHeader:   "X-Internal-Secret",                    // default
    TrustedProxies: []string{"10.0.0.0/8"},                 // gateway source CIDRs
})

api.GET("/billing", billingHandler.Get, trusted)
api.POST("/billing", billingHandler.Create, trusted)
```

Requests that fail any configured gate get no subject, so downstream checks fail closed. `TrustedProxies` is matched against `c.RealIP()`; configure `server.WithTrustedProxies` so `X-Forwarded-For` can't be spoofed. Same `GetSubjectID(c)` API as session-based auth.

> [!CAUTION]
> `hamrmw.TrustedSubject()` (no config) trusts `X-Subject-ID` from anyone. If any untrusted client can reach the route, one spoofed header is a full authentication bypass. Use it only on a fully internal network.
