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
	err      error                    // if non-nil, GetByToken returns this
}

func newMockStore() *mockSessionStore {
	return &mockSessionStore{sessions: make(map[string]*auth.Session)}
}

func (m *mockSessionStore) Create(_ context.Context, s *auth.Session) error {
	m.sessions[s.Token] = s
	return nil
}

func (m *mockSessionStore) GetByToken(_ context.Context, token string) (*auth.Session, error) {
	if m.err != nil {
		return nil, m.err
	}
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
	if subjectID == "deleted" {
		return nil, nil
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

// newAuth creates a BrowserAuth with the given options for tests.
func newAuth(mgr *auth.SessionManager, opts ...middleware.BrowserAuthOption) *middleware.BrowserAuth {
	return middleware.NewBrowserAuth(mgr, opts...)
}

// ---------------------------------------------------------------------------
// Load tests
// ---------------------------------------------------------------------------

func TestLoad_validSession(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-1")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	ba := newAuth(mgr, middleware.WithSubjectLoader(testSubjectLoader))

	var subjectID string
	var subject any
	handler := ba.Load()(func(c echo.Context) error {
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

func TestLoad_noSubjectLoaderSetsIDOnly(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-2")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	ba := newAuth(mgr)

	var subjectID string
	var subject any
	handler := ba.Load()(func(c echo.Context) error {
		subjectID = middleware.GetSubjectID(c)
		subject = middleware.GetSubject(c)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "user-2", subjectID)
	assert.Nil(t, subject)
}

func TestLoad_noCookie(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	ba := newAuth(mgr)

	var called bool
	handler := ba.Load()(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, middleware.GetSubjectID(c))
}

func TestLoad_invalidSession(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "bad-token")

	ba := newAuth(mgr)

	var called bool
	handler := ba.Load()(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called, "Load must not block on invalid session")

	// Stale cookie must be cleared so the browser stops sending it.
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "expected a Set-Cookie header to clear the stale cookie")
	assert.Equal(t, "session_token", cookies[0].Name)
	assert.Equal(t, -1, cookies[0].MaxAge)
}

func TestLoad_subjectLoaderError(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "error") // triggers loader error
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	ba := newAuth(mgr, middleware.WithSubjectLoader(testSubjectLoader))

	handler := ba.Load()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loader error")
}

func TestLoad_validateSessionError(t *testing.T) {
	store, mgr, c, rec := setupAuthTest(t, "")
	store.err = errors.New("db connection refused")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: "any-token"})

	ba := newAuth(mgr)

	handler := ba.Load()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db connection refused")

	// Cookie must NOT be cleared — the failure is transient, not an invalid session.
	cookies := rec.Result().Cookies()
	assert.Empty(t, cookies, "cookie should not be cleared on DB error")
}

func TestLoad_nilSubjectClearsAuthState(t *testing.T) {
	store, mgr, c, rec := setupAuthTest(t, "")
	token := createTestSession(store, "deleted") // loader returns (nil, nil)
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	ba := newAuth(mgr, middleware.WithSubjectLoader(testSubjectLoader))

	var called bool
	handler := ba.Load()(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called, "Load must continue when subject is nil")
	assert.Empty(t, middleware.GetSubjectID(c), "SubjectIDKey must not be set for deleted subject")
	assert.Nil(t, middleware.GetSubject(c), "SubjectKey must not be set for deleted subject")

	// Stale cookie must be cleared.
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "session_token", cookies[0].Name)
	assert.Equal(t, -1, cookies[0].MaxAge)
}

func TestLoad_setsSessionKey(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-5")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	ba := newAuth(mgr)

	var session any
	handler := ba.Load()(func(c echo.Context) error {
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

// ---------------------------------------------------------------------------
// RequireAuth tests
// ---------------------------------------------------------------------------

func TestRequireAuth_redirectsWhenNoSubject(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	ba := newAuth(mgr)
	handler := ba.RequireAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestRequireAuth_passesWhenSubject(t *testing.T) {
	store, mgr, c, _ := setupAuthTest(t, "")
	token := createTestSession(store, "user-1")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	ba := newAuth(mgr, middleware.WithSubjectLoader(testSubjectLoader))

	// Run Load first to populate ctx.
	load := ba.Load()(func(c echo.Context) error { return nil })
	require.NoError(t, load(c))

	// Fresh recorder so RequireAuth assertions aren't tainted by Load.
	rec2 := httptest.NewRecorder()
	c.SetResponse(echo.NewResponse(rec2, c.Echo()))

	handler := ba.RequireAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "ok", rec2.Body.String())
}

func TestRequireAuth_customRedirect(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	ba := newAuth(mgr, middleware.WithLoginRedirect("/auth/signin"))
	handler := ba.RequireAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/auth/signin", rec.Header().Get("Location"))
}

func TestRequireAuth_hxRedirect(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")
	c.Request().Header.Set("HX-Request", "true")

	ba := newAuth(mgr, middleware.WithHXRedirect())
	handler := ba.RequireAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("HX-Redirect"))
	assert.Empty(t, rec.Body.String(), "HX-Redirect response must have empty body")
}

func TestRequireAuth_hxRedirectFallsBackFor303(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")
	// No HX-Request header — normal browser navigation.

	ba := newAuth(mgr, middleware.WithHXRedirect())
	handler := ba.RequireAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	assert.Empty(t, rec.Header().Get("HX-Redirect"))
}

// ---------------------------------------------------------------------------
// RequireNotAuth tests
// ---------------------------------------------------------------------------

func TestRequireNotAuth_redirectsWhenSubject(t *testing.T) {
	store, mgr, c, rec := setupAuthTest(t, "")
	token := createTestSession(store, "user-4")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})

	ba := newAuth(mgr)

	// Run Load first to populate ctx.
	load := ba.Load()(func(c echo.Context) error { return nil })
	require.NoError(t, load(c))

	handler := ba.RequireNotAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/dashboard", rec.Header().Get("Location"))
}

func TestRequireNotAuth_passesWhenNoSubject(t *testing.T) {
	_, mgr, c, rec := setupAuthTest(t, "")

	ba := newAuth(mgr)

	var called bool
	handler := ba.RequireNotAuth()(func(c echo.Context) error {
		called = true
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireNotAuth_hxRedirect(t *testing.T) {
	store, mgr, c, rec := setupAuthTest(t, "")
	token := createTestSession(store, "user-4")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})
	c.Request().Header.Set("HX-Request", "true")

	ba := newAuth(mgr, middleware.WithHXRedirect())

	// Run Load first to populate ctx.
	load := ba.Load()(func(c echo.Context) error { return nil })
	require.NoError(t, load(c))

	handler := ba.RequireNotAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/dashboard", rec.Header().Get("HX-Redirect"))
	assert.Empty(t, rec.Body.String(), "HX-Redirect response must have empty body")
}

func TestRequireNotAuth_hxRedirectFallsBackFor303(t *testing.T) {
	store, mgr, c, rec := setupAuthTest(t, "")
	token := createTestSession(store, "user-4")
	c.Request().AddCookie(&http.Cookie{Name: "session_token", Value: token})
	// No HX-Request header — normal browser navigation.

	ba := newAuth(mgr, middleware.WithHXRedirect())

	// Run Load first to populate ctx.
	load := ba.Load()(func(c echo.Context) error { return nil })
	require.NoError(t, load(c))

	handler := ba.RequireNotAuth()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/dashboard", rec.Header().Get("Location"))
	assert.Empty(t, rec.Header().Get("HX-Redirect"))
}
