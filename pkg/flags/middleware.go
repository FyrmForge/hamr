package flags

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/labstack/echo/v4"
)

// WhoFunc resolves the request's user. Run Middleware after auth.
type WhoFunc func(c echo.Context) Who

// Middleware puts c and the request's Who on the request context, and adds
// every flag's value to the request logger as a "flags" group.
func (c *Client) Middleware(who WhoFunc) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ec echo.Context) error {
			w := who(ec)
			anon := ""
			if w.SubjectID == "" && c.anonCookie != "" {
				anon = c.anonID(ec)
			}
			r := ec.Request()
			ctx := withScope(r.Context(), c, w, anon)
			sc := ctx.Value(scopeKey{}).(scope)
			defs := Definitions()
			attrs := make([]any, 0, len(defs))
			for _, d := range defs {
				v, ok := c.value(sc, d.Key)
				if !ok {
					v = registryDefault(d.Key)
				}
				attrs = append(attrs, slog.Any(d.Key, v))
			}
			ctx = logging.With(ctx, slog.Group("flags", attrs...))
			ec.SetRequest(r.WithContext(ctx))
			return next(ec)
		}
	}
}

func registryDefault(key string) any {
	d, _ := lookup(key)
	if d == nil {
		return nil
	}
	return d.def
}

// anonID returns the visitor's anonymous ID, setting the cookie if absent.
func (c *Client) anonID(ec echo.Context) string {
	if ck, err := ec.Request().Cookie(c.anonCookie); err == nil && ck.Value != "" {
		return ck.Value
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	ec.SetCookie(&http.Cookie{
		Name:     c.anonCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   ec.Scheme() == "https", // honours X-Forwarded-Proto behind a TLS proxy
		MaxAge:   int(365 * 24 * time.Hour / time.Second),
	})
	return id
}
