package middleware

import (
	"net/http"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"

	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/FyrmForge/hamr/pkg/respond"
)

// ErrorPage is a function that returns a templ component for an error.
type ErrorPage func(code int, message string) templ.Component

// PageOverride maps a specific status code to an ErrorPage.
type PageOverride struct {
	Code int
	Page ErrorPage
}

// Page creates a per-status-code override.
func Page(code int, page ErrorPage) PageOverride {
	return PageOverride{Code: code, Page: page}
}

// ErrorPages returns middleware that catches errors and renders error pages.
// The default ErrorPage handles all codes unless overridden by Page() entries.
func ErrorPages(defaultPage ErrorPage, overrides ...PageOverride) echo.MiddlewareFunc {
	lookup := make(map[int]ErrorPage, len(overrides))
	for _, o := range overrides {
		lookup[o.Code] = o.Page
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)
			if err == nil {
				return nil
			}

			if c.Response().Committed {
				return err
			}

			code := http.StatusInternalServerError
			message := http.StatusText(code)

			if he, ok := err.(*echo.HTTPError); ok {
				code = he.Code
				if m, ok := he.Message.(string); ok {
					message = m
				} else {
					message = http.StatusText(code)
				}
			}

			if code >= http.StatusInternalServerError {
				logging.FromContext(c.Request().Context()).Error("server error",
					"error", err, "status", code)
			}

			page := defaultPage
			if p, ok := lookup[code]; ok {
				page = p
			}

			return respond.HTML(c, code, page(code, message))
		}
	}
}
