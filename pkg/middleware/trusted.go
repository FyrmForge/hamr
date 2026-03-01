package middleware

import (
	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/labstack/echo/v4"
)

const headerSubjectID = "X-Subject-ID"

// TrustedSubject reads the X-Subject-ID header from trusted internal requests
// and sets it in the context. Handlers can then use GetSubjectID(c) to
// retrieve the subject ID, exactly as they would with session-based auth.
//
// Use this middleware for services behind a gateway or main service that
// forwards the authenticated subject's identity. It should only be used on
// internal networks, never exposed publicly.
//
//	e.Use(middleware.TrustedSubject())
func TrustedSubject() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			subjectID := c.Request().Header.Get(headerSubjectID)
			if subjectID != "" {
				ctx.Set(c, ctx.SubjectIDKey, subjectID)
			}
			return next(c)
		}
	}
}
