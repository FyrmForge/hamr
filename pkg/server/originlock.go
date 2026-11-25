package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
)

// DefaultOriginSecretHeader is the header WithOriginSecret checks when
// OriginSecretConfig.Header is empty.
const DefaultOriginSecretHeader = "X-Origin-Secret"

// OriginSecretConfig configures WithOriginSecret.
type OriginSecretConfig struct {
	// Secret is the shared secret the proxy/CDN adds to every request.
	// Empty turns the feature off.
	Secret string

	// Header is the request header carrying the secret. Empty uses
	// DefaultOriginSecretHeader.
	Header string

	// ExemptPaths are let through without the secret (platform health
	// checks). Matched exactly against the request path: no prefixes, no
	// case folding, no trailing-slash tolerance, no percent-encoded variants.
	ExemptPaths []string
}

// WithOriginSecret refuses (403, empty body) every request that does not carry
// the shared secret header, so the app only answers traffic that came through
// your CDN/proxy, which adds the header. It runs before routing and static
// files, compares in constant time, and is independent of client-IP handling.
// Nothing about the secret or request headers is ever logged. Empty Secret
// (the default) is a no-op.
func WithOriginSecret(cfg OriginSecretConfig) Option {
	return func(s *Server) { s.originSecret = cfg }
}

func originHeader(cfg OriginSecretConfig) string {
	if cfg.Header == "" {
		return DefaultOriginSecretHeader
	}
	return cfg.Header
}

func originLock(cfg OriginSecretConfig) echo.MiddlewareFunc {
	header := originHeader(cfg)
	secret := []byte(cfg.Secret)
	exempt := make(map[string]struct{}, len(cfg.ExemptPaths))
	for _, p := range cfg.ExemptPaths {
		exempt[p] = struct{}{}
	}
	var warn sync.Once
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			r := c.Request()
			if subtle.ConstantTimeCompare([]byte(r.Header.Get(header)), secret) == 1 {
				return next(c)
			}
			// Echo routes on RawPath when set, so an encoded look-alike of an
			// exempt path must not pass.
			if _, ok := exempt[r.URL.Path]; ok && (r.URL.RawPath == "" || r.URL.RawPath == r.URL.Path) {
				return next(c)
			}
			warn.Do(func() {
				slog.Default().Warn("server: origin lock refused a request (further refusals not logged)",
					"method", r.Method, "path", r.URL.Path)
			})
			return c.NoContent(http.StatusForbidden)
		}
	}
}
