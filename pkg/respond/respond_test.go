package respond

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock templ.Component
// ---------------------------------------------------------------------------

type mockComponent struct {
	html string
	err  error
}

func (m *mockComponent) Render(_ context.Context, w io.Writer) error {
	if m.err != nil {
		return m.err
	}
	_, err := io.WriteString(w, m.html)
	return err
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func newTestContext(method, path string, headers map[string]string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	r := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	return e.NewContext(r, w), w
}

// ---------------------------------------------------------------------------
// HTML
// ---------------------------------------------------------------------------

func TestHTML_rendersComponent(t *testing.T) {
	c, w := newTestContext(http.MethodGet, "/", nil)
	comp := &mockComponent{html: "<h1>Hello</h1>"}

	err := HTML(c, http.StatusOK, comp)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "<h1>Hello</h1>", w.Body.String())
}

func TestHTML_componentError(t *testing.T) {
	c, w := newTestContext(http.MethodGet, "/", nil)
	comp := &mockComponent{err: errors.New("render failed")}

	err := HTML(c, http.StatusOK, comp)
	require.Error(t, err)
	// On a render failure nothing must be committed — no body and no
	// Content-Type header — so Echo's error handler can still send an error
	// page instead of a truncated 200.
	assert.Empty(t, w.Body.String())
	assert.Empty(t, w.Header().Get("Content-Type"))
}

// ---------------------------------------------------------------------------
// JSON
// ---------------------------------------------------------------------------

func TestJSON_sendsJSON(t *testing.T) {
	c, w := newTestContext(http.MethodGet, "/", nil)
	data := map[string]string{"key": "value"}

	err := JSON(c, http.StatusOK, data)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, w.Body.String(), `"key":"value"`)
}

// ---------------------------------------------------------------------------
// Redirect
// ---------------------------------------------------------------------------

func TestRedirect_htmxRequest(t *testing.T) {
	c, w := newTestContext(http.MethodPost, "/", map[string]string{"HX-Request": "true"})

	err := Redirect(c, "/dashboard")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "/dashboard", w.Header().Get("HX-Redirect"))
}

func TestRedirect_regularRequest(t *testing.T) {
	c, w := newTestContext(http.MethodPost, "/", nil)

	err := Redirect(c, "/dashboard")
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/dashboard", w.Header().Get("Location"))
}
