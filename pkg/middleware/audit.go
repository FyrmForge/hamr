package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/labstack/echo/v4"
)

// sensitiveParamSubstrings are case-insensitive substrings of query/path
// parameter NAMES whose VALUES are redacted before an audit entry is
// persisted. Errs toward over-redaction — an audit sink should never hold
// reset tokens, API keys, OTPs, or auth codes lifted from a URL.
var sensitiveParamSubstrings = []string{
	"token", "password", "passwd", "pwd", "secret", "key", "code",
	"otp", "signature", "sig", "auth", "bearer", "session", "csrf",
	"jwt", "nonce", "cvv", "ssn", "credential",
}

// isSensitiveParam reports whether a parameter name looks secret.
func isSensitiveParam(name string) bool {
	n := strings.ToLower(name)
	for _, s := range sensitiveParamSubstrings {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// redactQuery returns the raw query string with the values of sensitive-named
// parameters replaced. Returns "[unparseable]" if the query can't be parsed.
func redactQuery(raw string) string {
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return "[unparseable]"
	}
	for k, vs := range vals {
		if isSensitiveParam(k) {
			for i := range vs {
				vs[i] = "[REDACTED]"
			}
		}
	}
	return vals.Encode()
}

// AuditEntry records a single auditable action.
type AuditEntry struct {
	ActorID    string         `json:"actor_id"`
	Action     string         `json:"action"`      // HTTP method
	EntityType string         `json:"entity_type"`  // route path pattern
	Data       map[string]any `json:"data"`
	Timestamp  time.Time      `json:"timestamp"`
}

// AuditLogger persists audit entries. Projects implement this interface to
// store entries in their database, log aggregator, etc.
type AuditLogger interface {
	Log(ctx context.Context, entry *AuditEntry) error
}

// AuditConfig configures audit middleware.
type AuditConfig struct {
	Logger      AuditLogger
	ActorIDFunc func(c echo.Context) string // default: GetSubjectID
}

// Audit returns middleware that logs non-GET mutations via the given logger.
func Audit(logger AuditLogger) echo.MiddlewareFunc {
	return AuditWithConfig(AuditConfig{Logger: logger})
}

// AuditWithConfig returns audit middleware with the given config.
func AuditWithConfig(cfg AuditConfig) echo.MiddlewareFunc {
	if cfg.ActorIDFunc == nil {
		cfg.ActorIDFunc = GetSubjectID
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)

			if c.Request().Method == http.MethodGet {
				return err
			}

			data := map[string]any{
				"method": c.Request().Method,
				"path":   c.Request().URL.Path,
				"status": c.Response().Status,
			}

			if q := c.Request().URL.RawQuery; q != "" {
				data["query"] = redactQuery(q)
			}

			if names := c.ParamNames(); len(names) > 0 {
				params := make(map[string]string, len(names))
				for _, n := range names {
					if isSensitiveParam(n) {
						params[n] = "[REDACTED]"
					} else {
						params[n] = c.Param(n)
					}
				}
				data["path_params"] = params
			}

			entry := &AuditEntry{
				ActorID:    cfg.ActorIDFunc(c),
				Action:     c.Request().Method,
				EntityType: c.Path(),
				Data:       data,
				Timestamp:  time.Now(),
			}

			if logErr := cfg.Logger.Log(c.Request().Context(), entry); logErr != nil {
				logger := logging.FromContext(c.Request().Context())
				logger.Error("audit log failed",
					slog.String("error", logErr.Error()),
				)
			}

			return err
		}
	}
}
