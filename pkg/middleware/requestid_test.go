package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestID_generatesUUID(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.RequestID()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	// Response should have X-Request-ID header.
	rid := rec.Header().Get(middleware.HeaderRequestID)
	assert.NotEmpty(t, rid)
	assert.Len(t, rid, 36) // UUID format

	// Context should have the same ID.
	ctxID, ok := ctx.Get(c, ctx.RequestIDKey)
	assert.True(t, ok)
	assert.Equal(t, rid, ctxID)
}

func TestRequestID_usesExistingHeader(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	req.Header.Set(middleware.HeaderRequestID, "existing-id-123")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.RequestID()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	assert.Equal(t, "existing-id-123", rec.Header().Get(middleware.HeaderRequestID))
	ctxID, _ := ctx.Get(c, ctx.RequestIDKey)
	assert.Equal(t, "existing-id-123", ctxID)
}

func TestRequestID_logsRequest(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	// Inject a logger so FromContext finds one.
	reqCtx := logging.WithLogger(req.Context(), logging.New(false))
	req = req.WithContext(reqCtx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	var loggerFound bool
	handler := middleware.RequestID()(func(c echo.Context) error {
		l := logging.FromContext(c.Request().Context())
		loggerFound = l != nil
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, loggerFound)
}

func TestRequestID_setsContextKey(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	var gotID string
	handler := middleware.RequestID()(func(c echo.Context) error {
		gotID, _ = ctx.Get(c, ctx.RequestIDKey)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.NotEmpty(t, gotID)
}

func TestRequestID_skipsStaticLogging(t *testing.T) {
	// This test verifies that /static paths still get a request ID
	// but the path doesn't cause a panic or error.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.RequestID()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	// Should still get a request ID.
	rid := rec.Header().Get(middleware.HeaderRequestID)
	assert.NotEmpty(t, rid)
}
