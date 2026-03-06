// Package respond provides content-negotiated HTTP response helpers.
//
// It detects htmx requests, negotiates between HTML and JSON responses,
// and renders templ components or JSON payloads via Echo.
package respond

import (
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"

	"github.com/FyrmForge/hamr/pkg/htmx"
)

// wantsHTML determines if the client prefers an HTML response.
// Priority: HX-Request header → Accept header → default to JSON.
func wantsHTML(c echo.Context) bool {
	if htmx.IsHTMX(c.Request()) {
		return true
	}
	accept := c.Request().Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

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

// Negotiate sends HTML or JSON based on client preference.
func Negotiate(c echo.Context, status int, jsonData any, component templ.Component) error {
	if wantsHTML(c) {
		return HTML(c, status, component)
	}
	return JSON(c, status, jsonData)
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type validationResponse struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields"`
}

// Error sends an error response, negotiating between HTML and JSON.
// An optional templ component renders the HTML error page.
func Error(c echo.Context, status int, msg string, component ...templ.Component) error {
	if wantsHTML(c) {
		if len(component) > 0 && component[0] != nil {
			return HTML(c, status, component[0])
		}
		return c.String(status, msg)
	}
	return c.JSON(status, errorResponse{
		Error:   http.StatusText(status),
		Message: msg,
		Code:    status,
	})
}

// ValidationError sends a 422 validation error response.
// An optional templ component renders the HTML error view.
func ValidationError(c echo.Context, fields map[string]string, component ...templ.Component) error {
	if wantsHTML(c) && len(component) > 0 && component[0] != nil {
		return HTML(c, http.StatusUnprocessableEntity, component[0])
	}
	return c.JSON(http.StatusUnprocessableEntity, validationResponse{
		Error:  "Validation failed",
		Fields: fields,
	})
}
