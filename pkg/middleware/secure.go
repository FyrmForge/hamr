package middleware

import (
	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// SecureConfig allows overriding the Content-Security-Policy header.
type SecureConfig struct {
	ContentSecurityPolicy string
}

// Secure returns security headers middleware with framework defaults.
func Secure() echo.MiddlewareFunc {
	return SecureWithConfig(SecureConfig{})
}

// SecureWithConfig returns security headers middleware with the given config.
func SecureWithConfig(cfg SecureConfig) echo.MiddlewareFunc {
	csp := cfg.ContentSecurityPolicy
	if csp == "" {
		csp = "default-src 'self'"
	}

	return echoMw.SecureWithConfig(echoMw.SecureConfig{
		XSSProtection:         "0",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "DENY",
		ContentSecurityPolicy: csp,
		ReferrerPolicy:        "strict-origin-when-cross-origin",
	})
}
