package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock audit logger
// ---------------------------------------------------------------------------

type mockAuditLogger struct {
	mu      sync.Mutex
	entries []*middleware.AuditEntry
	err     error
}

func (m *mockAuditLogger) Log(_ context.Context, entry *middleware.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return m.err
}

func (m *mockAuditLogger) lastEntry() *middleware.AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return nil
	}
	return m.entries[len(m.entries)-1]
}

func (m *mockAuditLogger) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAudit_logsMutation(t *testing.T) {
	logger := &mockAuditLogger{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/users", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/users")

	handler := middleware.Audit(logger)(func(c echo.Context) error {
		return c.String(http.StatusCreated, "created")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, 1, logger.count())

	entry := logger.lastEntry()
	assert.Equal(t, "POST", entry.Action)
	assert.Equal(t, "/users", entry.EntityType)
	assert.NotZero(t, entry.Timestamp)
}

func TestAudit_skipsGET(t *testing.T) {
	logger := &mockAuditLogger{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := middleware.Audit(logger)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, 0, logger.count())
}

func TestAudit_capturesActorID(t *testing.T) {
	logger := &mockAuditLogger{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/users/42", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/users/:id")
	ctx.Set(c, ctx.SubjectIDKey, "actor-123")

	handler := middleware.Audit(logger)(func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	err := handler(c)
	require.NoError(t, err)

	entry := logger.lastEntry()
	require.NotNil(t, entry)
	assert.Equal(t, "actor-123", entry.ActorID)
}

func TestAudit_customActorIDFunc(t *testing.T) {
	logger := &mockAuditLogger{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/settings", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/settings")

	handler := middleware.AuditWithConfig(middleware.AuditConfig{
		Logger: logger,
		ActorIDFunc: func(c echo.Context) string {
			return "custom-actor"
		},
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	entry := logger.lastEntry()
	require.NotNil(t, entry)
	assert.Equal(t, "custom-actor", entry.ActorID)
}

func TestAudit_logErrorNonFatal(t *testing.T) {
	logger := &mockAuditLogger{err: errors.New("db down")}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/orders")

	handler := middleware.Audit(logger)(func(c echo.Context) error {
		return c.String(http.StatusCreated, "created")
	})

	err := handler(c)
	// Request should still succeed even though audit logging failed.
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestAudit_redactsSensitiveParams(t *testing.T) {
	logger := &mockAuditLogger{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/reset?token=abc123&email=a@b.com", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/reset/:reset_token")
	c.SetParamNames("reset_token")
	c.SetParamValues("supersecret")

	handler := middleware.Audit(logger)(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	require.NoError(t, handler(c))

	entry := logger.lastEntry()
	require.NotNil(t, entry)

	q, _ := entry.Data["query"].(string)
	assert.NotContains(t, q, "abc123", "sensitive token value must not be persisted")
	assert.Contains(t, q, "REDACTED")
	assert.Contains(t, q, "a%40b.com", "non-sensitive params preserved")

	params, _ := entry.Data["path_params"].(map[string]string)
	require.NotNil(t, params)
	assert.Equal(t, "[REDACTED]", params["reset_token"])
}
