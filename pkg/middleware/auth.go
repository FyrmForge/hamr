package middleware

import (
	"context"
	"net/http"

	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/labstack/echo/v4"
)

// SubjectLoader loads a subject by ID. Projects provide their own
// implementation (e.g. loading a User from the database).
type SubjectLoader func(ctx context.Context, subjectID string) (any, error)

// AuthConfig configures session-based authentication middleware.
type AuthConfig struct {
	SessionManager *auth.SessionManager
	SubjectLoader  SubjectLoader // optional — if nil, only SubjectIDKey is set
	LoginRedirect  string        // default: "/login"
	HomeRedirect   string        // default: "/dashboard"
}

func (cfg AuthConfig) withDefaults() AuthConfig {
	if cfg.LoginRedirect == "" {
		cfg.LoginRedirect = "/login"
	}
	if cfg.HomeRedirect == "" {
		cfg.HomeRedirect = "/dashboard"
	}
	return cfg
}

// Auth returns middleware that requires a valid session. Returns 401 on failure.
func Auth(cfg AuthConfig) echo.MiddlewareFunc {
	cfg = cfg.withDefaults()
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if err := authenticateSession(c, cfg); err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
			}
			return next(c)
		}
	}
}

// RequireAuth returns middleware that requires a valid session.
// Redirects to the login page on failure (browser-style).
func RequireAuth(cfg AuthConfig) echo.MiddlewareFunc {
	cfg = cfg.withDefaults()
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if err := authenticateSession(c, cfg); err != nil {
				return c.Redirect(http.StatusSeeOther, cfg.LoginRedirect)
			}
			return next(c)
		}
	}
}

// OptionalAuth populates the context if the user is logged in but never
// blocks the request.
func OptionalAuth(cfg AuthConfig) echo.MiddlewareFunc {
	cfg = cfg.withDefaults()
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			_ = authenticateSession(c, cfg)
			return next(c)
		}
	}
}

// RequireNotAuth redirects already-authenticated users to the home page.
func RequireNotAuth(cfg AuthConfig) echo.MiddlewareFunc {
	cfg = cfg.withDefaults()
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if err := authenticateSession(c, cfg); err == nil {
				return c.Redirect(http.StatusSeeOther, cfg.HomeRedirect)
			}
			return next(c)
		}
	}
}

// authenticateSession validates the session cookie, loads the subject, and
// populates the Echo context. Returns a non-nil error on any failure.
func authenticateSession(c echo.Context, cfg AuthConfig) error {
	cookie, err := c.Cookie(cfg.SessionManager.CookieName())
	if err != nil {
		return err
	}

	session, err := cfg.SessionManager.ValidateSession(c.Request().Context(), cookie.Value)
	if err != nil || session == nil {
		if err != nil {
			return err
		}
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	ctx.Set(c, ctx.SubjectIDKey, session.SubjectID)
	ctx.Set(c, ctx.SessionKey, any(session))

	if cfg.SubjectLoader != nil {
		subject, err := cfg.SubjectLoader(c.Request().Context(), session.SubjectID)
		if err != nil {
			return err
		}
		ctx.Set(c, ctx.SubjectKey, subject)
	}

	// Enrich the request logger with subject_id.
	reqCtx := logging.With(c.Request().Context(), "subject_id", session.SubjectID)
	c.SetRequest(c.Request().WithContext(reqCtx))

	return nil
}
