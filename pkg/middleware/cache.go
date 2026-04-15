package middleware

import (
	"fmt"
	"strings"

	"github.com/FyrmForge/hamr/pkg/fingerprint"
	"github.com/labstack/echo/v4"
)

// DefaultImmutableExtensions are file extensions treated as immutable assets.
var DefaultImmutableExtensions = []string{
	".webp", ".jpg", ".jpeg", ".png", ".gif", ".svg", ".ico",
	".woff2", ".woff", ".ttf", ".eot",
}

// DefaultStaticExtensions are file extensions treated as cacheable static assets.
var DefaultStaticExtensions = []string{".css", ".js"}

// CacheConfig configures the CacheControlWithConfig middleware.
type CacheConfig struct {
	// ImmutableExtensions are file extensions cached as immutable.
	// Default: DefaultImmutableExtensions.
	ImmutableExtensions []string

	// ImmutableMaxAge is the max-age in seconds for immutable assets.
	// Default: 31536000 (1 year).
	ImmutableMaxAge int

	// StaticExtensions are file extensions cached with a shorter TTL.
	// Default: DefaultStaticExtensions.
	StaticExtensions []string

	// StaticMaxAge is the max-age in seconds for static assets.
	// Default: 86400 (1 day).
	StaticMaxAge int

	// DisableCaching sets no-cache directives on every response.
	DisableCaching bool
}

// CacheControl sets Cache-Control headers based on asset type.
// When disableCaching is true every response gets no-cache directives.
func CacheControl(disableCaching bool) echo.MiddlewareFunc {
	return CacheControlWithConfig(CacheConfig{DisableCaching: disableCaching})
}

// CacheControlWithConfig sets Cache-Control headers using the given config.
func CacheControlWithConfig(cfg CacheConfig) echo.MiddlewareFunc {
	if cfg.ImmutableExtensions == nil {
		cfg.ImmutableExtensions = DefaultImmutableExtensions
	}
	if cfg.StaticExtensions == nil {
		cfg.StaticExtensions = DefaultStaticExtensions
	}
	if cfg.ImmutableMaxAge <= 0 {
		cfg.ImmutableMaxAge = 31536000
	}
	if cfg.StaticMaxAge <= 0 {
		cfg.StaticMaxAge = 86400
	}

	// Pre-compute header values.
	immutableHeader := fmt.Sprintf("public, max-age=%d, immutable", cfg.ImmutableMaxAge)
	staticHeader := fmt.Sprintf("public, max-age=%d", cfg.StaticMaxAge)

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if cfg.DisableCaching {
				c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				return next(c)
			}

			path := c.Request().URL.Path
			switch {
			case strings.HasPrefix(path, "/static/") && fingerprint.IsFingerprinted(path):
				c.Response().Header().Set("Cache-Control", immutableHeader)
			case hasSuffix(path, cfg.ImmutableExtensions):
				c.Response().Header().Set("Cache-Control", immutableHeader)
			case hasSuffix(path, cfg.StaticExtensions):
				c.Response().Header().Set("Cache-Control", staticHeader)
			}

			return next(c)
		}
	}
}

// hasSuffix reports whether path ends with any of the given extensions.
func hasSuffix(path string, exts []string) bool {
	for _, ext := range exts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}
