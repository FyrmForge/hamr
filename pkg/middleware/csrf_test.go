package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSRF_defaultConfig(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.CSRF()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	// A GET request should set the CSRF cookie.
	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "csrf" {
			csrfCookie = cookie
			break
		}
	}
	require.NotNil(t, csrfCookie, "csrf cookie should be set on GET")
	assert.NotEmpty(t, csrfCookie.Value)
}
