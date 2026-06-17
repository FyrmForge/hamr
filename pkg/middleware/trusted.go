package middleware

import (
	"crypto/subtle"
	"net"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/labstack/echo/v4"
)

const (
	headerSubjectID     = "X-Subject-ID"
	defaultSecretHeader = "X-Internal-Secret"
)

// TrustedSubjectConfig gates which requests may establish the subject identity
// from the X-Subject-ID header. With neither field set the header is trusted
// unconditionally (the legacy behaviour) — only safe on a fully internal
// network where nothing untrusted can reach the mount.
type TrustedSubjectConfig struct {
	// SharedSecret, when non-empty, requires the request to present this exact
	// value in SecretHeader (constant-time compared) before X-Subject-ID is
	// honoured.
	SharedSecret string

	// SecretHeader carries SharedSecret. Default: "X-Internal-Secret".
	SecretHeader string

	// TrustedProxies, when non-empty, requires the direct peer IP (c.RealIP())
	// to fall within one of these CIDRs. Unparseable entries are dropped, so a
	// list with no valid CIDR can never satisfy the gate (fails closed).
	//
	// NOTE: this is only as trustworthy as c.RealIP(). Configure the server's
	// trusted-proxy IP extractor (server.WithTrustedProxies) so X-Forwarded-For
	// can't be spoofed; the default extractor uses the direct peer, which is safe.
	TrustedProxies []string
}

// TrustedSubject reads X-Subject-ID and sets it as the subject identity with
// NO verification — equivalent to TrustedSubjectWithConfig(TrustedSubjectConfig{}).
// Use only behind a gateway on a trusted internal network. Prefer
// TrustedSubjectWithConfig with a shared secret and/or trusted CIDRs whenever
// the mount could be reached by untrusted clients; otherwise a single spoofed
// header is a full authentication bypass.
//
//	trusted := middleware.TrustedSubject()
//	api.GET("/billing", billingHandler.Get, trusted)
func TrustedSubject() echo.MiddlewareFunc {
	return TrustedSubjectWithConfig(TrustedSubjectConfig{})
}

// TrustedSubjectWithConfig is TrustedSubject gated by a shared secret and/or
// trusted source CIDRs. When a gate is configured and the request fails it, the
// X-Subject-ID header is ignored (the subject is left unset) so downstream
// authorization fails closed.
func TrustedSubjectWithConfig(cfg TrustedSubjectConfig) echo.MiddlewareFunc {
	secretHeader := cfg.SecretHeader
	if secretHeader == "" {
		secretHeader = defaultSecretHeader
	}

	cidrGate := len(cfg.TrustedProxies) > 0
	var nets []*net.IPNet
	for _, c := range cfg.TrustedProxies {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !trustedRequest(c, cfg.SharedSecret, secretHeader, cidrGate, nets) {
				return next(c) // untrusted source: do not establish a subject
			}
			if subjectID := c.Request().Header.Get(headerSubjectID); subjectID != "" {
				ctx.Set(c, ctx.SubjectIDKey, subjectID)
			}
			return next(c)
		}
	}
}

// trustedRequest reports whether the request satisfies every configured gate.
// With no gate configured (no secret, no CIDRs) it returns true — the legacy
// ungated behaviour.
func trustedRequest(c echo.Context, secret, secretHeader string, cidrGate bool, nets []*net.IPNet) bool {
	if secret != "" {
		got := c.Request().Header.Get(secretHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			return false
		}
	}
	if cidrGate {
		ip := net.ParseIP(c.RealIP())
		if ip == nil {
			return false
		}
		inRange := false
		for _, n := range nets {
			if n.Contains(ip) {
				inRange = true
				break
			}
		}
		if !inRange {
			return false
		}
	}
	return true
}
