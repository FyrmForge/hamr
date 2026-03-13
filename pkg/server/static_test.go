package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func staticHandler(body string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.HTML(http.StatusOK, body)
	}
}

func TestStaticPage_registersForGeneration(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)

	srv.StaticPage("/about", staticHandler("<h1>About</h1>"))

	dir := t.TempDir()
	err = srv.GenerateStatic(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "about", "index.html"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "<h1>About</h1>")
}

func TestGenerateStatic_writesFiles(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)

	srv.StaticPage("/about", staticHandler("<h1>About</h1>"))
	srv.StaticPage("/terms", staticHandler("<h1>Terms</h1>"))

	dir := t.TempDir()
	err = srv.GenerateStatic(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "about", "index.html"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "<h1>About</h1>")

	data, err = os.ReadFile(filepath.Join(dir, "terms", "index.html"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "<h1>Terms</h1>")
}

func TestGenerateStatic_pathMapping(t *testing.T) {
	tests := []struct {
		path string
		file string
	}{
		{"/", "index.html"},
		{"/about", "about/index.html"},
		{"/a/b/c", "a/b/c/index.html"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			srv, err := server.New(server.WithDevMode(true))
			require.NoError(t, err)

			srv.StaticPage(tt.path, staticHandler("ok"))

			dir := t.TempDir()
			err = srv.GenerateStatic(dir)
			require.NoError(t, err)

			_, err = os.Stat(filepath.Join(dir, tt.file))
			assert.NoError(t, err, "expected file %s to exist", tt.file)
		})
	}
}

func TestGenerateStatic_removesStaleFiles(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)

	srv.StaticPage("/about", staticHandler("<h1>About</h1>"))

	dir := t.TempDir()

	// Seed a stale file from a previous generation run.
	oldDir := filepath.Join(dir, "old-page")
	require.NoError(t, os.MkdirAll(oldDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "index.html"), []byte("stale"), 0o644))

	err = srv.GenerateStatic(dir)
	require.NoError(t, err)

	// The newly registered page should exist.
	_, err = os.Stat(filepath.Join(dir, "about", "index.html"))
	assert.NoError(t, err)

	// The stale file should be gone.
	_, err = os.Stat(filepath.Join(oldDir, "index.html"))
	assert.True(t, os.IsNotExist(err), "stale file should have been removed")
}

func TestGeneratedMiddleware_servesFile(t *testing.T) {
	dir := t.TempDir()

	// Pre-create a generated file.
	aboutDir := filepath.Join(dir, "about")
	require.NoError(t, os.MkdirAll(aboutDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(aboutDir, "index.html"), []byte("<h1>Static About</h1>"), 0o644))

	srv, err := server.New(server.WithDevMode(true), server.WithGeneratedDir(dir))
	require.NoError(t, err)

	// Register a fallback handler that should NOT be reached.
	srv.GET("/about", func(c echo.Context) error {
		return c.String(http.StatusOK, "dynamic fallback")
	})

	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<h1>Static About</h1>")
}

func TestGeneratedMiddleware_fallsThrough(t *testing.T) {
	dir := t.TempDir() // Empty — no generated files.

	srv, err := server.New(server.WithDevMode(true), server.WithGeneratedDir(dir))
	require.NoError(t, err)

	srv.GET("/about", func(c echo.Context) error {
		return c.String(http.StatusOK, "dynamic")
	})

	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "dynamic", rec.Body.String())
}

func TestGeneratedMiddleware_GETOnly(t *testing.T) {
	dir := t.TempDir()

	// Pre-create a generated file for /about.
	aboutDir := filepath.Join(dir, "about")
	require.NoError(t, os.MkdirAll(aboutDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(aboutDir, "index.html"), []byte("static"), 0o644))

	srv, err := server.New(server.WithDevMode(true), server.WithGeneratedDir(dir))
	require.NoError(t, err)

	srv.POST("/about", func(c echo.Context) error {
		return c.String(http.StatusOK, "post handler")
	})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/about", nil)
			rec := httptest.NewRecorder()
			srv.Echo().ServeHTTP(rec, req)

			// Should NOT serve the static file for non-GET methods.
			assert.NotContains(t, rec.Body.String(), "static")
		})
	}
}
