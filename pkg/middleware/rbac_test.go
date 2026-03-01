package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rbacContext(subject any) echo.Context {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if subject != nil {
		ctx.Set(c, ctx.SubjectKey, subject)
	}
	return c
}

func alwaysAllow(_ any, _ []string) bool { return true }
func alwaysDeny(_ any, _ []string) bool  { return false }
func alwaysActive(_ any) bool            { return true }
func alwaysInactive(_ any) bool          { return false }

// ---------------------------------------------------------------------------
// RequireRoles
// ---------------------------------------------------------------------------

func TestRequireRoles_allowed(t *testing.T) {
	c := rbacContext(&testUser{ID: "1"})

	var called bool
	handler := middleware.RequireRoles(alwaysAllow, "admin")(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRequireRoles_forbidden(t *testing.T) {
	c := rbacContext(&testUser{ID: "1"})

	handler := middleware.RequireRoles(alwaysDeny, "admin")(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, he.Code)
}

func TestRequireRoles_noSubject(t *testing.T) {
	c := rbacContext(nil)

	handler := middleware.RequireRoles(alwaysAllow, "admin")(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, he.Code)
}

// ---------------------------------------------------------------------------
// RequireActive
// ---------------------------------------------------------------------------

func TestRequireActive_active(t *testing.T) {
	c := rbacContext(&testUser{ID: "1"})

	var called bool
	handler := middleware.RequireActive(alwaysActive)(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRequireActive_inactive(t *testing.T) {
	c := rbacContext(&testUser{ID: "1"})

	handler := middleware.RequireActive(alwaysInactive)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, he.Code)
}

func TestRequireActive_noSubject(t *testing.T) {
	c := rbacContext(nil)

	handler := middleware.RequireActive(alwaysActive)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, he.Code)
}
