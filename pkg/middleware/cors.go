package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// CORSConfig allows overriding CORS defaults.
type CORSConfig struct {
	AllowOrigins []string
	AllowMethods []string
	AllowHeaders []string
}

// CORS returns CORS middleware with framework defaults.
func CORS() echo.MiddlewareFunc {
	return CORSWithConfig(CORSConfig{})
}

// CORSWithConfig returns CORS middleware with the given config.
func CORSWithConfig(cfg CORSConfig) echo.MiddlewareFunc {
	origins := cfg.AllowOrigins
	if len(origins) == 0 {
		origins = []string{}
	}

	methods := cfg.AllowMethods
	if len(methods) == 0 {
		methods = []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodDelete,
			http.MethodOptions,
		}
	}

	headers := cfg.AllowHeaders
	if len(headers) == 0 {
		headers = []string{
			"Origin",
			"Content-Type",
			"Accept",
			"Authorization",
			"X-CSRF-Token",
			"HX-Request",
			"HX-Target",
			"HX-Trigger",
		}
	}

	return echoMw.CORSWithConfig(echoMw.CORSConfig{
		AllowOrigins: origins,
		AllowMethods: methods,
		AllowHeaders: headers,
	})
}
