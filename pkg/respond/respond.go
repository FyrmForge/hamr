// Package respond provides HTTP response helpers for HTMX-first applications.
//
// It renders templ components or JSON payloads via Echo. Use respond.HTML for
// templ components and respond.JSON for API responses.
package respond

import (
	"net/http"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"

	"github.com/FyrmForge/hamr/pkg/htmx"
)

// HTML renders a templ component with the given status code.
func HTML(c echo.Context, status int, component templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	c.Response().WriteHeader(status)
	return component.Render(c.Request().Context(), c.Response())
}

// JSON sends a JSON response with the given status code.
func JSON(c echo.Context, status int, data any) error {
	return c.JSON(status, data)
}

// Redirect sends an HTMX-aware redirect. For HTMX requests it sets the
// HX-Redirect header and returns 200; for regular requests it returns a 303.
func Redirect(c echo.Context, url string) error {
	if htmx.IsHTMX(c.Request()) {
		htmx.Redirect(c.Response(), url)
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusSeeOther, url)
}
