package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// CSRFConfig allows overriding CSRF defaults.
type CSRFConfig struct {
	CookieName  string // default: "csrf"
	TokenLookup string // default: "form:csrf_token,header:X-CSRF-Token"
	Secure      bool   // CSRF() sets true; CSRFWithConfig uses the value given (zero = false)
	// SameSite controls the cookie's SameSite attribute. Zero value defaults
	// to Lax (a sensible CSRF defense-in-depth) rather than the browser
	// default. Set explicitly to override (e.g. http.SameSiteStrictMode).
	SameSite http.SameSite
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
	// http.SameSite's zero value is 0 (unset); SameSiteDefaultMode is 1. Default
	// the unset case to Lax — comparing against SameSiteDefaultMode would miss
	// it and ship a cookie with no SameSite attribute at all.
	if cfg.SameSite == 0 {
		cfg.SameSite = http.SameSiteLaxMode
	}

	return echoMw.CSRFWithConfig(echoMw.CSRFConfig{
		CookieName:  cfg.CookieName,
		TokenLookup: cfg.TokenLookup,
		// Without Path the browser scopes the cookie to the directory of the
		// first URL that set it (/admin), so forms elsewhere lose it. "__Host-"
		// names are rejected unless Path=/.
		CookiePath:     "/",
		CookieSecure:   cfg.Secure,
		CookieSameSite: cfg.SameSite,
	})
}
