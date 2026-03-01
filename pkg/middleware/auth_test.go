package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock session store
// ---------------------------------------------------------------------------

type mockSessionStore struct {
	sessions map[string]*auth.Session // keyed by token
}

func newMockStore() *mockSessionStore {
	return &mockSessionStore{sessions: make(map[string]*auth.Session)}
}

func (m *mockSessionStore) Create(_ context.Context, s *auth.Session) error {
	m.sessions[s.Token] = s
	return nil
}

func (m *mockSessionStore) GetByToken(_ context.Context, token string) (*auth.Session, error) {
	s, ok := m.sessions[token]
	if !ok {
		return nil, nil
	}
	return s, nil
}

func (m *mockSessionStore) Delete(_ context.Context, id string) error {
	for tok, s := range m.sessions {
		if s.ID == id {
			delete(m.sessions, tok)
		}
	}
	return nil
}

func (m *mockSessionStore) DeleteBySubjectID(_ context.Context, subjectID string) error {
	for tok, s := range m.sessions {
		if s.SubjectID == subjectID {
			delete(m.sessions, tok)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mock subject loader
// ---------------------------------------------------------------------------

type testUser struct {
	ID   string
	Name string
}

func testSubjectLoader(_ context.Context, subjectID string) (any, error) {
	if subjectID == "error" {
		return nil, errors.New("loader error")
	}
	return &testUser{ID: subjectID, Name: "Test User"}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func setupAuthTest(t *testing.T, token string) (*mockSessionStore, *auth.SessionManager, echo.Context, *httptest.ResponseRecorder) {
	t.Helper()

	store := newMockStore()
	mgr := auth.NewSessionManager(store, auth.WithCookieName("session_token"))

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	return store, mgr, c, rec
}

func createTestSession(store *mockSessionStore, subjectID string) string {
	token := "test-token-" + subjectID
	store.sessions[token] = &auth.Session{
		ID:        "session-" + subjectID,
		SubjectID: subjectID,
		Token:     token,
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}
	return token
}

// ---------------------------------------------------------------------------
// Auth tests
// ---------------------------------------------------------------------------

func TestAuth_validSession(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-1")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	var subjectID string
	var subject any
	handler := middleware.Auth(middleware.AuthConfig{
		SessionManager: mgr,
		SubjectLoader:  testSubjectLoader,
	})(func(c echo.Context) error {
		subjectID = middleware.GetSubjectID(c)
		subject = middleware.GetSubject(c)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "user-1", subjectID)
	require.NotNil(t, subject)
	assert.Equal(t, "user-1", subject.(*testUser).ID)
}

func TestAuth_noSessionLoaderSetsIDOnly(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-2")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	var subjectID string
	var subject any
	handler := middleware.Auth(middleware.AuthConfig{
		SessionManager: mgr,
		// SubjectLoader is nil
	})(func(c echo.Context) error {
		subjectID = middleware.GetSubjectID(c)
		subject = middleware.GetSubject(c)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "user-2", subjectID)
	assert.Nil(t, subject)
}

func TestAuth_invalidSession(t *testing.T) {
	_, mgr, c, _ := setupAuthTest(t, "bad-token")

	handler := middleware.Auth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)

	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, he.Code)
}

func TestAuth_noCookie(t *testing.T) {
	_, mgr, c, _ := setupAuthTest(t, "")

	handler := middleware.Auth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)

	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, he.Code)
}

// ---------------------------------------------------------------------------
// RequireAuth tests
// ---------------------------------------------------------------------------

func TestRequireAuth_redirectsOnFailure(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	handler := middleware.RequireAuth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestRequireAuth_customRedirect(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	handler := middleware.RequireAuth(middleware.AuthConfig{
		SessionManager: mgr,
		LoginRedirect:  "/auth/signin",
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/auth/signin", rec.Header().Get("Location"))
}

// ---------------------------------------------------------------------------
// OptionalAuth tests
// ---------------------------------------------------------------------------

func TestOptionalAuth_validSession(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-3")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	var subjectID string
	handler := middleware.OptionalAuth(middleware.AuthConfig{
		SessionManager: mgr,
		SubjectLoader:  testSubjectLoader,
	})(func(c echo.Context) error {
		subjectID = middleware.GetSubjectID(c)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "user-3", subjectID)
}

func TestOptionalAuth_noCookie(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	var called bool
	handler := middleware.OptionalAuth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOptionalAuth_invalidSession(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "bad-token")

	var called bool
	handler := middleware.OptionalAuth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// ---------------------------------------------------------------------------
// RequireNotAuth tests
// ---------------------------------------------------------------------------

func TestRequireNotAuth_authenticated(t *testing.T) {
	store, mgr, c, rec := setupAuthTest(t, "")
	token := createTestSession(store, "user-4")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	handler := middleware.RequireNotAuth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/dashboard", rec.Header().Get("Location"))
}

func TestRequireNotAuth_notAuthenticated(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	var called bool
	handler := middleware.RequireNotAuth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// ---------------------------------------------------------------------------
// SessionKey test
// ---------------------------------------------------------------------------

func TestAuth_setsSessionKey(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-5")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	var session any
	handler := middleware.Auth(middleware.AuthConfig{
		SessionManager: mgr,
	})(func(c echo.Context) error {
		s, ok := ctx.Get(c, ctx.SessionKey)
		if ok {
			session = s
		}
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	require.NotNil(t, session)

	s, ok := session.(*auth.Session)
	require.True(t, ok)
	assert.Equal(t, "user-5", s.SubjectID)
}
