package middleware

import (
	"context"
	"net/http"

	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/FyrmForge/hamr/pkg/htmx"
	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/labstack/echo/v4"
)

// SubjectLoader loads a subject by ID. Projects provide their own
// implementation (e.g. loading a User from the database).
type SubjectLoader func(ctx context.Context, subjectID string) (any, error)

// BrowserAuth provides session-based authentication middleware split into a
// loader (DB access) and pure policy checks (ctx-only).
type BrowserAuth struct {
	sessionMgr    *auth.SessionManager
	subjectLoader SubjectLoader
	loginRedirect string
	homeRedirect  string
	hxRedirect    bool
}

// BrowserAuthOption configures a BrowserAuth instance.
type BrowserAuthOption func(*BrowserAuth)

// WithSubjectLoader sets a function that loads the full subject by ID.
// If not set, only SubjectIDKey and SessionKey are populated in context.
func WithSubjectLoader(loader SubjectLoader) BrowserAuthOption {
	return func(b *BrowserAuth) { b.subjectLoader = loader }
}

// WithLoginRedirect sets the URL unauthenticated users are redirected to.
// Default: "/login".
func WithLoginRedirect(url string) BrowserAuthOption {
	return func(b *BrowserAuth) { b.loginRedirect = url }
}

// WithHomeRedirect sets the URL authenticated users are redirected away to
// (e.g. from login/register pages). Default: "/dashboard".
func WithHomeRedirect(url string) BrowserAuthOption {
	return func(b *BrowserAuth) { b.homeRedirect = url }
}

// WithHXRedirect makes policy middleware respond with an HX-Redirect header
// instead of a 303 Location redirect, for HTMX-driven navigations.
func WithHXRedirect() BrowserAuthOption {
	return func(b *BrowserAuth) { b.hxRedirect = true }
}

// NewBrowserAuth creates a BrowserAuth with the given session manager and options.
func NewBrowserAuth(sm *auth.SessionManager, opts ...BrowserAuthOption) *BrowserAuth {
	b := &BrowserAuth{
		sessionMgr:    sm,
		loginRedirect: "/login",
		homeRedirect:  "/dashboard",
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Load returns middleware that validates the session cookie and populates the
// Echo context. This is the only middleware that touches the database.
//
// Behavior:
//   - No cookie → next (no subject in ctx)
//   - Cookie present, invalid/expired → clear cookie, next (no subject in ctx)
//   - DB error on ValidateSession or SubjectLoader → return error (500)
//   - Valid session → set SubjectIDKey, SessionKey, SubjectKey in ctx, enrich logger
func (b *BrowserAuth) Load() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cookie, err := c.Cookie(b.sessionMgr.CookieName())
			if err != nil {
				// No cookie — continue without auth state.
				return next(c)
			}

			session, err := b.sessionMgr.ValidateSession(c.Request().Context(), cookie.Value)
			if err != nil {
				return err
			}
			if session == nil {
				// Invalid/expired — clear the stale cookie and continue.
				b.clearCookie(c)
				return next(c)
			}

			if b.subjectLoader != nil {
				subject, err := b.subjectLoader(c.Request().Context(), session.SubjectID)
				if err != nil {
					return err
				}
				if subject == nil {
					// Subject was deleted — treat session as stale.
					b.clearCookie(c)
					return next(c)
				}
				ctx.Set(c, ctx.SubjectKey, subject)
			}

			ctx.Set(c, ctx.SubjectIDKey, session.SubjectID)
			ctx.Set(c, ctx.SessionKey, any(session))

			// Enrich the request logger with subject_id.
			reqCtx := logging.With(c.Request().Context(), "subject_id", session.SubjectID)
			c.SetRequest(c.Request().WithContext(reqCtx))

			return next(c)
		}
	}
}

// RequireAuth returns middleware that requires an authenticated subject in the
// context. Must be mounted after Load(). No database calls.
//
// No subject in ctx → redirect to LoginRedirect (303 or HX-Redirect).
func (b *BrowserAuth) RequireAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if GetSubjectID(c) == "" {
				return b.redirect(c, b.loginRedirect)
			}
			return next(c)
		}
	}
}

// RequireNotAuth returns middleware that redirects already-authenticated users
// away (e.g. from login/register pages). Must be mounted after Load(). No
// database calls.
//
// Subject in ctx → redirect to HomeRedirect (303 or HX-Redirect).
func (b *BrowserAuth) RequireNotAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if GetSubjectID(c) != "" {
				return b.redirect(c, b.homeRedirect)
			}
			return next(c)
		}
	}
}

// clearCookie sets a cookie-clearing header so the browser stops sending the
// stale session cookie.
func (b *BrowserAuth) clearCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     b.sessionMgr.CookieName(),
		Path:     b.sessionMgr.CookiePath(),
		Domain:   b.sessionMgr.CookieDomain(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   b.sessionMgr.CookieSecure(),
		SameSite: b.sessionMgr.SameSite(),
	})
}

// redirect sends a 303 redirect or sets the HX-Redirect header depending on
// the hxRedirect flag.
func (b *BrowserAuth) redirect(c echo.Context, url string) error {
	if b.hxRedirect && htmx.IsHTMX(c.Request()) {
		c.Response().Header().Set("HX-Redirect", url)
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusSeeOther, url)
}
