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
	// Unset SameSite must default to Lax, not be omitted (defense-in-depth).
	assert.Equal(t, http.SameSiteLaxMode, csrfCookie.SameSite,
		"default CSRF cookie must carry SameSite=Lax")
}

func TestCSRF_explicitSameSite(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.CSRFWithConfig(middleware.CSRFConfig{
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	require.NoError(t, handler(c))

	var csrfCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "csrf" {
			csrfCookie = cookie
			break
		}
	}
	require.NotNil(t, csrfCookie)
	assert.Equal(t, http.SameSiteStrictMode, csrfCookie.SameSite,
		"explicit SameSite must be honored")
}
