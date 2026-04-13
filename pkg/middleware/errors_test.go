package middleware_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FyrmForge/hamr/pkg/middleware"
)

// stubPage returns a simple ErrorPage that renders "<error CODE: MSG>".
func stubPage(code int, message string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<error "+http.StatusText(code)+": "+message+">")
		return err
	})
}

func stubAltPage(code int, message string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<custom "+http.StatusText(code)+": "+message+">")
		return err
	})
}

func TestErrorPages_handlerError(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := middleware.ErrorPages(stubPage)
	handler := mw(func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusNotFound, "page not found")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "<error Not Found: page not found>")
}

func TestErrorPages_defaultsTo500(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := middleware.ErrorPages(stubPage)
	handler := mw(func(c echo.Context) error {
		return errors.New("unexpected")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Internal Server Error")
}

func TestErrorPages_override(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := middleware.ErrorPages(stubPage,
		middleware.Page(http.StatusNotFound, stubAltPage),
	)
	handler := mw(func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusNotFound, "gone")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "<custom Not Found: gone>")
}

func TestErrorPages_noOverrideFallsToDefault(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := middleware.ErrorPages(stubPage,
		middleware.Page(http.StatusNotFound, stubAltPage),
	)
	handler := mw(func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusForbidden, "denied")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "<error Forbidden: denied>")
}

func TestErrorPages_noErrorPassesThrough(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := middleware.ErrorPages(stubPage)
	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestErrorPages_committedResponseSkipped(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	mw := middleware.ErrorPages(stubPage)
	handler := mw(func(c echo.Context) error {
		_ = c.String(http.StatusOK, "already sent")
		return echo.NewHTTPError(http.StatusNotFound, "too late")
	})

	err := handler(c)
	// Error is returned as-is when response is committed.
	assert.Error(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
}
