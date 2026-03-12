package middleware

import (
	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
)

// SecureConfig allows overriding security response headers.
// Zero-value fields use sensible defaults.
type SecureConfig struct {
	ContentSecurityPolicy string // default: "default-src 'self'"
	XFrameOptions         string // default: "DENY"
	ReferrerPolicy        string // default: "strict-origin-when-cross-origin"
	XSSProtection         string // default: "0"
	ContentTypeNosniff    string // default: "nosniff"
}

// Secure returns security headers middleware with framework defaults.
func Secure() echo.MiddlewareFunc {
	return SecureWithConfig(SecureConfig{})
}

// SecureWithConfig returns security headers middleware with the given config.
func SecureWithConfig(cfg SecureConfig) echo.MiddlewareFunc {
	if cfg.ContentSecurityPolicy == "" {
		cfg.ContentSecurityPolicy = "default-src 'self'"
	}
	if cfg.XFrameOptions == "" {
		cfg.XFrameOptions = "DENY"
	}
	if cfg.ReferrerPolicy == "" {
		cfg.ReferrerPolicy = "strict-origin-when-cross-origin"
	}
	if cfg.XSSProtection == "" {
		cfg.XSSProtection = "0"
	}
	if cfg.ContentTypeNosniff == "" {
		cfg.ContentTypeNosniff = "nosniff"
	}

	return echoMw.SecureWithConfig(echoMw.SecureConfig{
		XSSProtection:         cfg.XSSProtection,
		ContentTypeNosniff:    cfg.ContentTypeNosniff,
		XFrameOptions:         cfg.XFrameOptions,
		ContentSecurityPolicy: cfg.ContentSecurityPolicy,
		ReferrerPolicy:        cfg.ReferrerPolicy,
	})
}
