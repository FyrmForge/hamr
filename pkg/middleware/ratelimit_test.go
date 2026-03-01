package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// MemoryStore tests
// ---------------------------------------------------------------------------

func TestMemoryStore_allowsWithinRate(t *testing.T) {
	store := middleware.NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		allowed, _, _, err := store.Allow(ctx, "key1", 10, time.Minute)
		require.NoError(t, err)
		assert.True(t, allowed, "request %d should be allowed", i+1)
	}
}

func TestMemoryStore_deniesOverRate(t *testing.T) {
	store := middleware.NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		allowed, _, _, err := store.Allow(ctx, "key1", 10, time.Minute)
		require.NoError(t, err)
		assert.True(t, allowed)
	}
	// 11th should be denied.
	allowed, _, _, err := store.Allow(ctx, "key1", 10, time.Minute)
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestMemoryStore_windowReset(t *testing.T) {
	store := middleware.NewMemoryStore()
	ctx := context.Background()

	// Fill up the window.
	for i := 0; i < 10; i++ {
		_, _, _, err := store.Allow(ctx, "key1", 10, 50*time.Millisecond)
		require.NoError(t, err)
	}

	// Wait for the window to expire.
	time.Sleep(60 * time.Millisecond)

	allowed, _, _, err := store.Allow(ctx, "key1", 10, 50*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, allowed, "should be allowed after window reset")
}

func TestMemoryStore_differentKeys(t *testing.T) {
	store := middleware.NewMemoryStore()
	ctx := context.Background()

	// Fill key1.
	for i := 0; i < 10; i++ {
		_, _, _, err := store.Allow(ctx, "key1", 10, time.Minute)
		require.NoError(t, err)
	}
	denied, _, _, err := store.Allow(ctx, "key1", 10, time.Minute)
	require.NoError(t, err)
	assert.False(t, denied)

	// key2 should still be allowed.
	allowed, _, _, err := store.Allow(ctx, "key2", 10, time.Minute)
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestMemoryStore_remaining(t *testing.T) {
	store := middleware.NewMemoryStore()
	ctx := context.Background()

	_, remaining, _, err := store.Allow(ctx, "key1", 5, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 4, remaining) // 5 - 1

	_, remaining, _, err = store.Allow(ctx, "key1", 5, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 3, remaining) // 5 - 2
}

// ---------------------------------------------------------------------------
// Middleware tests
// ---------------------------------------------------------------------------

func TestRateLimit_setsHeaders(t *testing.T) {
	store := middleware.NewMemoryStore()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-Ip", "1.2.3.4")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.RateLimit(store)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	assert.NotEmpty(t, rec.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, rec.Header().Get("X-RateLimit-Reset"))
}

func TestRateLimit_returns429(t *testing.T) {
	store := middleware.NewMemoryStore()
	e := echo.New()

	// Use a very low rate to trigger 429.
	mw := middleware.RateLimitWithConfig(middleware.RateLimitConfig{
		Store:         store,
		Rate: 1,
		Burst:         0,
		KeyFunc: func(c echo.Context) (string, error) {
			return "test-key", nil
		},
	})

	// First request should pass (count=1, rate=1+0=1).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	err := handler(c)
	require.NoError(t, err)

	// Second request should be denied.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)
	handler2 := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	err2 := handler2(c2)
	require.Error(t, err2)

	he, ok := err2.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusTooManyRequests, he.Code)
}

func TestRateLimit_customKeyFunc(t *testing.T) {
	store := middleware.NewMemoryStore()
	e := echo.New()

	mw := middleware.RateLimitWithConfig(middleware.RateLimitConfig{
		Store:         store,
		Rate: 1,
		Burst:         0,
		KeyFunc: func(c echo.Context) (string, error) {
			return c.Request().Header.Get("X-API-Key"), nil
		},
	})

	// key-a: first request.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "key-a")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	err := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})(c)
	require.NoError(t, err)

	// key-a: second request — should be denied.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-API-Key", "key-a")
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)
	err2 := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})(c2)
	require.Error(t, err2)

	// key-b: should still work.
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("X-API-Key", "key-b")
	rec3 := httptest.NewRecorder()
	c3 := e.NewContext(req3, rec3)
	err3 := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})(c3)
	require.NoError(t, err3)
}

func TestRateLimit_defaultsToIP(t *testing.T) {
	store := middleware.NewMemoryStore()
	e := echo.New()

	mw := middleware.RateLimitWithConfig(middleware.RateLimitConfig{
		Store:         store,
		Rate: 2,
		Burst:         0,
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Real-Ip", "10.0.0.1")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		err := mw(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})(c)
		require.NoError(t, err)

		remaining, _ := strconv.Atoi(rec.Header().Get("X-RateLimit-Remaining"))
		assert.Equal(t, 2-(i+1), remaining)
	}
}
