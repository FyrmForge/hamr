package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// With no AllowOrigins configured, CORS() denies cross-origin requests: no
// Access-Control-Allow-Origin header is emitted (rather than the old "*").
func TestCORS_deniesWhenNoOriginsConfigured(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.CORS()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	require.NoError(t, handler(c))
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"unconfigured CORS must not allow an arbitrary origin")
}

// With explicit origins, the framework default headers (incl. HTMX + CSRF) are
// advertised for the allowed origin.
func TestCORS_defaultHeadersWithExplicitOrigin(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"http://example.com"},
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	require.NoError(t, handler(c))
	assert.Equal(t, "http://example.com", rec.Header().Get("Access-Control-Allow-Origin"))

	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"HX-Request", "HX-Target", "HX-Trigger", "X-CSRF-Token"} {
		assert.True(t, strings.Contains(allowHeaders, h),
			"expected %q in Access-Control-Allow-Headers, got %q", h, allowHeaders)
	}
}
