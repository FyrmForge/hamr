package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// CORSConfig allows overriding CORS defaults.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
}

// CORS returns CORS middleware with framework defaults.
func CORS() echo.MiddlewareFunc {
	return CORSWithConfig(CORSConfig{})
}

// CORSWithConfig returns CORS middleware with the given config.
//
// When AllowOrigins is empty, cross-origin requests are DENIED (no
// Access-Control-Allow-Origin header) rather than falling back to Echo's
// permissive "*" default — apps that need CORS must pass explicit origins.
func CORSWithConfig(cfg CORSConfig) echo.MiddlewareFunc {
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

	ecfg := echoMw.CORSConfig{
		AllowMethods:     methods,
		AllowHeaders:     headers,
		AllowCredentials: cfg.AllowCredentials,
	}
	if len(cfg.AllowOrigins) == 0 {
		// Deny all cross-origin requests. AllowOriginFunc takes precedence over
		// AllowOrigins at request time, so returning false never emits an
		// Access-Control-Allow-Origin header (Echo would otherwise default the
		// empty list to "*").
		ecfg.AllowOriginFunc = func(string) (bool, error) { return false, nil }
	} else {
		ecfg.AllowOrigins = cfg.AllowOrigins
	}
	return echoMw.CORSWithConfig(ecfg)
}
