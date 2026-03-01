// Package middleware provides HTTP middleware for the HAMR framework.
package middleware

import (
	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/labstack/echo/v4"
)

// GetSubjectID returns the authenticated subject's ID from the request context.
// Works with both session-based auth (Auth middleware) and trusted header auth
// (TrustedSubject middleware). Returns empty string if no subject is set.
func GetSubjectID(c echo.Context) string {
	id, _ := ctx.Get(c, ctx.SubjectIDKey)
	return id
}

// GetSubject returns the loaded subject from the request context.
// Only populated when a SubjectLoader is configured (session-based auth).
// Returns nil if no subject is loaded.
func GetSubject(c echo.Context) any {
	subject, _ := ctx.Get(c, ctx.SubjectKey)
	return subject
}
