package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// RoleChecker returns true if the subject has at least one of the given roles.
type RoleChecker func(subject any, roles []string) bool

// ActiveChecker returns true if the subject's account is active.
type ActiveChecker func(subject any) bool

// RequireRoles returns middleware that checks whether the authenticated subject
// has one of the required roles. Returns 401 if no subject is present or 403
// if the role check fails.
func RequireRoles(checker RoleChecker, roles ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			subject := GetSubject(c)
			if subject == nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
			}
			if !checker(subject, roles) {
				return echo.NewHTTPError(http.StatusForbidden, "forbidden")
			}
			return next(c)
		}
	}
}

// RequireActive returns middleware that checks whether the authenticated
// subject's account is active. Returns 401 if no subject is present or 403 if
// the account is not active.
func RequireActive(checker ActiveChecker) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			subject := GetSubject(c)
			if subject == nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
			}
			if !checker(subject) {
				return echo.NewHTTPError(http.StatusForbidden, "account not active")
			}
			return next(c)
		}
	}
}
