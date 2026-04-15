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

func TestCacheControl_immutableAssets(t *testing.T) {
	extensions := []string{
		".webp", ".jpg", ".jpeg", ".png", ".gif", ".svg", ".ico",
		".woff2", ".woff", ".ttf", ".eot",
	}

	for _, ext := range extensions {
		t.Run(ext, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/static/file"+ext, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := middleware.CacheControl(false)(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)
			assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestCacheControl_staticAssets(t *testing.T) {
	for _, ext := range []string{".css", ".js"} {
		t.Run(ext, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/static/file"+ext, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := middleware.CacheControl(false)(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)
			assert.Equal(t, "public, max-age=86400", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestCacheControl_htmlPage(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.CacheControl(false)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Empty(t, rec.Header().Get("Cache-Control"))
}

func TestCacheControl_disableCaching(t *testing.T) {
	paths := []string{"/static/file.css", "/static/logo.png", "/dashboard"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := middleware.CacheControl(true)(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)
			assert.Equal(t, "no-cache, no-store, must-revalidate", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestCacheControl_fingerprintedAssets(t *testing.T) {
	paths := []string{
		"/static/css/output.a1b2c3d4e5f6.css",
		"/static/js/main.789012345678.js",
		"/static/img/logo.abcdef123456.png",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := middleware.CacheControl(false)(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)
			assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestCacheControlWithConfig_customExtensions(t *testing.T) {
	cfg := middleware.CacheConfig{
		ImmutableExtensions: []string{".avif"},
		StaticExtensions:    []string{".xml"},
	}

	tests := []struct {
		path string
		want string
	}{
		{"/img/photo.avif", "public, max-age=31536000, immutable"},
		{"/feed.xml", "public, max-age=86400"},
		{"/static/logo.png", ""},  // .png not in custom immutable list
		{"/static/app.css", ""},   // .css not in custom static list
		{"/dashboard", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := middleware.CacheControlWithConfig(cfg)(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)
			assert.Equal(t, tt.want, rec.Header().Get("Cache-Control"))
		})
	}
}

func TestCacheControlWithConfig_customDurations(t *testing.T) {
	cfg := middleware.CacheConfig{
		ImmutableMaxAge: 3600,
		StaticMaxAge:    600,
	}

	e := echo.New()

	// Immutable asset with custom duration.
	req := httptest.NewRequest(http.MethodGet, "/img/logo.png", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	handler := middleware.CacheControlWithConfig(cfg)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	require.NoError(t, handler(c))
	assert.Equal(t, "public, max-age=3600, immutable", rec.Header().Get("Cache-Control"))

	// Static asset with custom duration.
	req = httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	require.NoError(t, handler(c))
	assert.Equal(t, "public, max-age=600", rec.Header().Get("Cache-Control"))
}

func TestCacheControlWithConfig_disableCaching(t *testing.T) {
	cfg := middleware.CacheConfig{DisableCaching: true}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.CacheControlWithConfig(cfg)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	require.NoError(t, handler(c))
	assert.Equal(t, "no-cache, no-store, must-revalidate", rec.Header().Get("Cache-Control"))
}
