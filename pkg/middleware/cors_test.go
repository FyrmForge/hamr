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

func TestCORS_defaultHeaders(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.CORS()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"HX-Request", "HX-Target", "HX-Trigger", "X-CSRF-Token"} {
		assert.True(t, strings.Contains(allowHeaders, h),
			"expected %q in Access-Control-Allow-Headers, got %q", h, allowHeaders)
	}
}
