package middleware

import (
	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// CSRFConfig allows overriding CSRF defaults.
type CSRFConfig struct {
	CookieName  string
	TokenLookup string
	Secure      bool
}

// CSRF returns CSRF protection middleware with framework defaults.
func CSRF() echo.MiddlewareFunc {
	return CSRFWithConfig(CSRFConfig{})
}

// CSRFWithConfig returns CSRF protection middleware with the given config.
func CSRFWithConfig(cfg CSRFConfig) echo.MiddlewareFunc {
	cookieName := cfg.CookieName
	if cookieName == "" {
		cookieName = "csrf"
	}

	tokenLookup := cfg.TokenLookup
	if tokenLookup == "" {
		tokenLookup = "form:csrf_token,header:X-CSRF-Token"
	}

	secure := cfg.Secure
	if !secure && cfg.CookieName == "" {
		// Only default to true when using zero-value config.
		secure = true
	}

	return echoMw.CSRFWithConfig(echoMw.CSRFConfig{
		CookieName:  cookieName,
		TokenLookup: tokenLookup,
		CookieSecure: secure,
	})
}
