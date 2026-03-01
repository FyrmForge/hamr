package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_productionDefaults(t *testing.T) {
	srv, err := server.New()
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

func TestNew_recoverMiddleware(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	srv.GET("/panic", func(c echo.Context) error {
		panic("test panic")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRouteGET(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	srv.GET("/hello", func(c echo.Context) error {
		return c.String(http.StatusOK, "get")
	})

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "get", rec.Body.String())
}

func TestRoutePOST(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	srv.POST("/hello", func(c echo.Context) error {
		return c.String(http.StatusCreated, "post")
	})

	req := httptest.NewRequest(http.MethodPost, "/hello", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "post", rec.Body.String())
}

func TestRoutePUT(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	srv.PUT("/hello", func(c echo.Context) error {
		return c.String(http.StatusOK, "put")
	})

	req := httptest.NewRequest(http.MethodPut, "/hello", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "put", rec.Body.String())
}

func TestRouteDELETE(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	srv.DELETE("/hello", func(c echo.Context) error {
		return c.String(http.StatusOK, "delete")
	})

	req := httptest.NewRequest(http.MethodDelete, "/hello", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "delete", rec.Body.String())
}

func TestRoutePATCH(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	srv.PATCH("/hello", func(c echo.Context) error {
		return c.String(http.StatusOK, "patch")
	})

	req := httptest.NewRequest(http.MethodPatch, "/hello", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "patch", rec.Body.String())
}

func TestGroup(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	g := srv.Group("/api")
	g.GET("/items", func(c echo.Context) error {
		return c.String(http.StatusOK, "items")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "items", rec.Body.String())
}

func TestGroup_withMiddleware(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)
	groupMw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("X-Group", "yes")
			return next(c)
		}
	}

	g := srv.Group("/api", groupMw)
	g.GET("/items", func(c echo.Context) error {
		return c.String(http.StatusOK, "items")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "yes", rec.Header().Get("X-Group"))
}

func TestEcho_escapeHatch(t *testing.T) {
	srv, err := server.New(server.WithDevMode(true))
	require.NoError(t, err)

	// Register route directly on the Echo instance.
	srv.Echo().GET("/direct", func(c echo.Context) error {
		return c.String(http.StatusOK, "direct")
	})

	req := httptest.NewRequest(http.MethodGet, "/direct", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "direct", rec.Body.String())
}

func TestAddr(t *testing.T) {
	tests := []struct {
		name string
		opts []server.Option
		want string
	}{
		{"defaults", nil, ":8080"},
		{"host only", []server.Option{server.WithHost("0.0.0.0")}, "0.0.0.0:8080"},
		{"port only", []server.Option{server.WithPort(9090)}, ":9090"},
		{"both", []server.Option{server.WithHost("localhost"), server.WithPort(3000)}, "localhost:3000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := server.New(tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, srv.Addr())
		})
	}
}
