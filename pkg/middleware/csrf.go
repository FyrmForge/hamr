package middleware

import (
	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// CSRFConfig allows overriding CSRF defaults.
type CSRFConfig struct {
	CookieName  string // default: "csrf"
	TokenLookup string // default: "form:csrf_token,header:X-CSRF-Token"
	Secure      bool   // default: true
}

// CSRF returns CSRF protection middleware with framework defaults.
func CSRF() echo.MiddlewareFunc {
	return CSRFWithConfig(CSRFConfig{Secure: true})
}

// CSRFWithConfig returns CSRF protection middleware with the given config.
func CSRFWithConfig(cfg CSRFConfig) echo.MiddlewareFunc {
	if cfg.CookieName == "" {
		cfg.CookieName = "csrf"
	}
	if cfg.TokenLookup == "" {
		cfg.TokenLookup = "form:csrf_token,header:X-CSRF-Token"
	}

	return echoMw.CSRFWithConfig(echoMw.CSRFConfig{
		CookieName:   cfg.CookieName,
		TokenLookup:  cfg.TokenLookup,
		CookieSecure: cfg.Secure,
	})
}
