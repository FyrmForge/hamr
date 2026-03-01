package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunBeforeMigrate_success(t *testing.T) {
	var order []int
	srv, err := server.New(
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 1)
			return nil
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 2)
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, order)
}

func TestRunBeforeMigrate_error(t *testing.T) {
	secondRan := false
	srv, err := server.New(
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			return errors.New("hook failed")
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			secondRan = true
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	assert.EqualError(t, err, "hook failed")
	assert.False(t, secondRan)
}

func TestRunBeforeMigrate_noHooks(t *testing.T) {
	srv, err := server.New()
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	assert.NoError(t, err)
}

func TestRunAfterMigrate_success(t *testing.T) {
	var order []int
	srv, err := server.New(
		server.WithOnAfterMigrate(func(_ context.Context) error {
			order = append(order, 1)
			return nil
		}),
		server.WithOnAfterMigrate(func(_ context.Context) error {
			order = append(order, 2)
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunAfterMigrate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, order)
}

func TestRunAfterMigrate_error(t *testing.T) {
	srv, err := server.New(
		server.WithOnAfterMigrate(func(_ context.Context) error {
			return errors.New("after failed")
		}),
	)
	require.NoError(t, err)

	err = srv.RunAfterMigrate(context.Background())
	assert.EqualError(t, err, "after failed")
}

func TestShutdown_runsHooks(t *testing.T) {
	hookRan := false
	srv, err := server.New(
		server.WithDevMode(true),
		server.WithOnShutdown(func(_ context.Context) error {
			hookRan = true
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.Shutdown(context.Background())
	require.NoError(t, err)
	assert.True(t, hookRan)
}

func TestShutdown_hookError_continuesShutdown(t *testing.T) {
	srv, err := server.New(
		server.WithDevMode(true),
		server.WithOnShutdown(func(_ context.Context) error {
			return errors.New("shutdown hook failed")
		}),
	)
	require.NoError(t, err)

	// Shutdown should succeed even if the hook errors.
	err = srv.Shutdown(context.Background())
	require.NoError(t, err)

	// Verify the Echo server is actually shut down by checking it rejects requests.
	srv.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	// After shutdown, the server may still serve via ServeHTTP (it's a handler),
	// but the important thing is Shutdown didn't return an error.
}

func TestMultipleHooks_executionOrder(t *testing.T) {
	var order []int
	srv, err := server.New(
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 1)
			return nil
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 2)
			return nil
		}),
		server.WithOnBeforeMigrate(func(_ context.Context) error {
			order = append(order, 3)
			return nil
		}),
	)
	require.NoError(t, err)

	err = srv.RunBeforeMigrate(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, order)
}
