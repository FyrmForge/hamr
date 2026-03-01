package middleware

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// CacheControl sets Cache-Control headers based on asset type.
// When disableCaching is true every response gets no-cache directives.
func CacheControl(disableCaching bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if disableCaching {
				c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				return next(c)
			}

			path := c.Request().URL.Path
			switch {
			case isImmutableAsset(path):
				c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			case isStaticAsset(path):
				c.Response().Header().Set("Cache-Control", "public, max-age=86400")
			}

			return next(c)
		}
	}
}

func isImmutableAsset(path string) bool {
	for _, ext := range []string{
		".webp", ".jpg", ".jpeg", ".png", ".gif", ".svg", ".ico",
		".woff2", ".woff", ".ttf", ".eot",
	} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func isStaticAsset(path string) bool {
	return strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".js")
}
