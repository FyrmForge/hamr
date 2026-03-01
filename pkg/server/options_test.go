package server_test

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/static
var testStaticFS embed.FS

func TestNew_defaults(t *testing.T) {
	srv, err := server.New()
	require.NoError(t, err)

	assert.Equal(t, ":8080", srv.Addr())
	assert.NotNil(t, srv.Echo())
}

func TestWithHost(t *testing.T) {
	srv, err := server.New(server.WithHost("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8080", srv.Addr())
}

func TestWithPort(t *testing.T) {
	srv, err := server.New(server.WithPort(3000))
	require.NoError(t, err)
	assert.Equal(t, ":3000", srv.Addr())
}

func TestWithPort_invalid(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too large", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.New(server.WithPort(tt.port))
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid port")
		})
	}
}

func TestWithDevMode_skipsSecure(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	srv.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("X-Content-Type-Options"))
}

func TestWithDevMode_false_appliesSecure(t *testing.T) {
	srv, err := server.New(server.WithDevMode(false))
	require.NoError(t, err)
	srv.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

func TestWithMiddleware(t *testing.T) {
	customMw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("X-Custom", "applied")
			return next(c)
		}
	}

	srv, err := server.New(server.WithDevMode(true), server.WithMiddleware(customMw))
	require.NoError(t, err)
	srv.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "applied", rec.Header().Get("X-Custom"))
}

func TestWithErrorHandler(t *testing.T) {
	handler := func(err error, c echo.Context) {
		_ = c.String(http.StatusTeapot, "custom error")
	}

	srv, err := server.New(server.WithDevMode(true), server.WithErrorHandler(handler))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTeapot, rec.Code)
	assert.Equal(t, "custom error", rec.Body.String())
}

func TestWithMaxBodySize(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true), server.WithMaxBodySize("10B"))
	require.NoError(t, err)
	srv.POST("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	body := strings.NewReader(strings.Repeat("x", 100))
	req := httptest.NewRequest(http.MethodPost, "/test", body)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestWithMaxBodySize_invalid(t *testing.T) {
	_, err := server.New(server.WithMaxBodySize("2X"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid max body size")
}

func TestWithTimeout_invalid(t *testing.T) {
	_, err := server.New(server.WithTimeout(0))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout must be positive")
}

func TestWithShutdownTimeout_invalid(t *testing.T) {
	_, err := server.New(server.WithShutdownTimeout(-1))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "shutdown timeout must be positive")
}

func TestWithStaticDir(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello static"), 0644)
	require.NoError(t, err)

	srv, err := server.New(server.WithDevMode(true), server.WithStaticDir(dir))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/static/hello.txt", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "hello static")
}

func TestWithEmbeddedStatic(t *testing.T) {
	srv, err := server.New(
		server.WithDevMode(true),
		server.WithEmbeddedStatic(echo.MustSubFS(testStaticFS, "testdata/static"), "/embedded"),
	)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/embedded/test.txt", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "hello from embedded static")
}

func TestWithEmbeddedStatic_emptyPrefix(t *testing.T) {
	_, err := server.New(
		server.WithEmbeddedStatic(echo.MustSubFS(testStaticFS, "testdata/static"), ""),
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embedded static path prefix must not be empty")
}
