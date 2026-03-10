# Authentication

HAMR uses server-side sessions (not JWTs) so sessions can be revoked instantly, and Argon2id for password hashing (the current OWASP recommendation). This guide covers the full auth stack: hashing, sessions, middleware, and login/register flows.

**Package references:** [Auth](pkg/auth.md), [Middleware](pkg/middleware.md) (auth sections)

---

## Password Hashing

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

---

## Session Management

### SessionStore Interface

Projects implement this for their database:

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

### Session Operations

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

---

## Auth Middleware

Configure auth middleware with your session manager and subject loader:

```go
cfg := middleware.AuthConfig{
    SessionManager: sm,
    SubjectLoader: func(ctx context.Context, id string) (any, error) {
        return repo.GetUser(ctx, id)
    },
    LoginRedirect: "/login",
    HomeRedirect:  "/dashboard",
}
```

### Four Variants

```go
// Require valid session — returns 401 on failure
protectedGroup.Use(middleware.Auth(cfg))

// Require valid session — redirects to login page on failure
siteGroup.Use(middleware.RequireAuth(cfg))

// Populate context if logged in, never block
publicGroup.Use(middleware.OptionalAuth(cfg))

// Redirect already-authenticated users away (login/register pages)
loginGroup.Use(middleware.RequireNotAuth(cfg))
```

### Reading Auth State in Handlers

```go
// Get the subject ID (string, works with both auth modes)
userID := middleware.GetSubjectID(c)

// Get the loaded subject (requires SubjectLoader)
user := middleware.GetSubject(c).(*models.User)
```

---

## Login Flow

```go
func (h *Handler) Login(c echo.Context) error {
    email := c.FormValue("email")
    password := c.FormValue("password")

    user, err := h.repo.GetByEmail(c.Request().Context(), email)
    if err != nil {
        return respond.Error(c, http.StatusUnauthorized, "Invalid credentials")
    }

    match, err := auth.CheckPassword(password, user.PasswordHash)
    if err != nil || !match {
        return respond.Error(c, http.StatusUnauthorized, "Invalid credentials")
    }

    session, err := h.sm.CreateSession(c.Request().Context(), user.ID, nil)
    if err != nil {
        return respond.Error(c, http.StatusInternalServerError, "Login failed")
    }

    c.SetCookie(&http.Cookie{
        Name:     h.sm.CookieName(),
        Value:    session.Token,
        Path:     h.sm.CookiePath(),
        Secure:   h.sm.CookieSecure(),
        SameSite: h.sm.SameSite(),
        Expires:  session.ExpiresAt,
        HttpOnly: true,
    })

    return c.Redirect(http.StatusSeeOther, "/dashboard")
}
```

## Register Flow

```go
func (h *Handler) Register(c echo.Context) error {
    name := c.FormValue("name")
    email := c.FormValue("email")
    password := c.FormValue("password")

    // Validate
    errors := map[string]string{}
    if msg := validate.Required(name); msg != "" {
        errors["name"] = msg
    }
    if msg := validate.Email(email); msg != "" {
        errors["email"] = msg
    }
    if msg := validate.PasswordStrength(password); msg != "" {
        errors["password"] = msg
    }
    if len(errors) > 0 {
        return respond.ValidationError(c, errors)
    }

    // Hash password
    hash, err := auth.HashPassword(password)
    if err != nil {
        return respond.Error(c, http.StatusInternalServerError, "Registration failed")
    }

    // Create user...
    middleware.SetFlash(c, "Account created! Please log in.", middleware.FlashSuccess)
    return c.Redirect(http.StatusSeeOther, "/login")
}
```

---

## Authorization (RBAC)

After authentication, restrict access by role:

```go
adminGroup.Use(middleware.RequireRoles(
    func(subject any, roles []string) bool {
        user := subject.(*models.User)
        return slices.Contains(roles, user.Role)
    },
    "admin", "superadmin",
))
```

Check active status:

```go
activeGroup.Use(middleware.RequireActive(
    func(subject any) bool {
        return subject.(*models.User).IsActive
    },
))
```

---

## Trusted Header Auth (Inter-Service)

When your app runs behind an API gateway (e.g., nginx, Traefik) that has already authenticated the request and forwards the subject ID:

```go
internalGroup.Use(middleware.TrustedSubject())
```

Reads `X-Subject-ID` header. Same `GetSubjectID(c)` API as session-based auth.

---

## Next Steps

- [File Storage](08-file-storage.md) — Upload and serve files
- [Deployment](13-deployment.md) — Production auth configuration
