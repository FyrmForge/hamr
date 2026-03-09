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
	c, _ := newTestContext(http.MethodGet, "/", nil)
	comp := &mockComponent{err: errors.New("render failed")}

	err := HTML(c, http.StatusOK, comp)
	assert.Error(t, err)
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
// Error
// ---------------------------------------------------------------------------

func TestError_jsonClient(t *testing.T) {
	c, w := newTestContext(http.MethodGet, "/", map[string]string{"Accept": "application/json"})

	err := Error(c, http.StatusBadRequest, "something broke")
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"error":"Bad Request"`)
	assert.Contains(t, w.Body.String(), `"message":"something broke"`)
}

func TestError_htmlWithComponent(t *testing.T) {
	c, w := newTestContext(http.MethodGet, "/", map[string]string{"Accept": "text/html"})
	comp := &mockComponent{html: "<p>error page</p>"}

	err := Error(c, http.StatusInternalServerError, "fail", comp)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "<p>error page</p>", w.Body.String())
}

func TestError_htmlWithoutComponent(t *testing.T) {
	c, w := newTestContext(http.MethodGet, "/", map[string]string{"Accept": "text/html"})

	err := Error(c, http.StatusNotFound, "not found")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "not found", w.Body.String())
}

func TestError_htmlNilComponent(t *testing.T) {
	c, w := newTestContext(http.MethodGet, "/", map[string]string{"Accept": "text/html"})

	err := Error(c, http.StatusNotFound, "not found", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "not found", w.Body.String())
}

// ---------------------------------------------------------------------------
// ValidationError
// ---------------------------------------------------------------------------

func TestValidationError_json(t *testing.T) {
	c, w := newTestContext(http.MethodPost, "/", nil)
	fields := map[string]string{"email": "required", "name": "too short"}

	err := ValidationError(c, fields)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"error":"Validation failed"`)
	assert.Contains(t, w.Body.String(), `"email":"required"`)
}

func TestValidationError_htmlWithComponent(t *testing.T) {
	c, w := newTestContext(http.MethodPost, "/", map[string]string{"Accept": "text/html"})
	comp := &mockComponent{html: "<div>errors</div>"}
	fields := map[string]string{"email": "required"}

	err := ValidationError(c, fields, comp)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "<div>errors</div>", w.Body.String())
}

func TestValidationError_htmlWithoutComponent(t *testing.T) {
	c, w := newTestContext(http.MethodPost, "/", map[string]string{"Accept": "text/html"})
	fields := map[string]string{"email": "required"}

	err := ValidationError(c, fields)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"error":"Validation failed"`)
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
