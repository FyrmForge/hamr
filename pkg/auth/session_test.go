package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock store
// ---------------------------------------------------------------------------

// mockStore is an in-memory SessionStore with error injection.
type mockStore struct {
	mu       sync.Mutex
	sessions map[string]*Session // keyed by ID

	createErr            error
	getByTokenErr        error
	deleteErr            error
	deleteBySubjectIDErr error
}

func newMockStore() *mockStore {
	return &mockStore{sessions: make(map[string]*Session)}
}

func (s *mockStore) Create(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	s.sessions[sess.ID] = sess
	return nil
}

func (s *mockStore) GetByToken(_ context.Context, token string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getByTokenErr != nil {
		return nil, s.getByTokenErr
	}
	for _, sess := range s.sessions {
		if sess.Token == token {
			return sess, nil
		}
	}
	return nil, nil
}

func (s *mockStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.sessions, id)
	return nil
}

func (s *mockStore) DeleteBySubjectID(_ context.Context, subjectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteBySubjectIDErr != nil {
		return s.deleteBySubjectIDErr
	}
	for id, sess := range s.sessions {
		if sess.SubjectID == subjectID {
			delete(s.sessions, id)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Default config
// ---------------------------------------------------------------------------

func TestSessionManagerDefaults(t *testing.T) {
	m := NewSessionManager(newMockStore())

	assert.Equal(t, 7*24*time.Hour, m.Duration())
	assert.Equal(t, "session_token", m.CookieName())
	assert.Equal(t, "/", m.CookiePath())
	assert.True(t, m.CookieSecure())
	assert.Equal(t, http.SameSiteLaxMode, m.SameSite())
}

func TestSessionManagerOptions(t *testing.T) {
	m := NewSessionManager(newMockStore(),
		WithDuration(1*time.Hour),
		WithCookieName("my_sess"),
		WithCookiePath("/app"),
		WithCookieSecure(false),
		WithSameSite(http.SameSiteStrictMode),
	)

	assert.Equal(t, 1*time.Hour, m.Duration())
	assert.Equal(t, "my_sess", m.CookieName())
	assert.Equal(t, "/app", m.CookiePath())
	assert.False(t, m.CookieSecure())
	assert.Equal(t, http.SameSiteStrictMode, m.SameSite())
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestSessionCreate(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	s, err := m.CreateSession(ctx, "user-42", nil)
	require.NoError(t, err)

	_, err = uuid.Parse(s.ID)
	assert.NoError(t, err, "ID should be a valid UUID")
	assert.NotEmpty(t, s.Token)
	assert.Equal(t, "user-42", s.SubjectID)
	assert.True(t, s.ExpiresAt.After(time.Now()), "ExpiresAt should be in the future")
}

func TestSessionCreateWithMetadata(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	meta := map[string]any{"ip": "10.0.0.1", "ua": "test-agent"}
	s, err := m.CreateSession(ctx, "user-1", meta)
	require.NoError(t, err)

	assert.Equal(t, "10.0.0.1", s.Metadata["ip"])
	assert.Equal(t, "test-agent", s.Metadata["ua"])

	// Validate round-trip preserves metadata.
	got, err := m.ValidateSession(ctx, s.Token)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "10.0.0.1", got.Metadata["ip"])
}

func TestSessionCreateTimestamps(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore(), WithDuration(2*time.Hour))

	before := time.Now()
	s, err := m.CreateSession(ctx, "user-1", nil)
	require.NoError(t, err)
	after := time.Now()

	assert.False(t, s.CreatedAt.Before(before), "CreatedAt should be >= before")
	assert.False(t, s.CreatedAt.After(after), "CreatedAt should be <= after")

	expected := s.CreatedAt.Add(2 * time.Hour)
	assert.Equal(t, expected, s.ExpiresAt)
}

func TestSessionCreateStoreError(t *testing.T) {
	store := newMockStore()
	store.createErr = errors.New("write failure")
	m := NewSessionManager(store)

	_, err := m.CreateSession(context.Background(), "user-1", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, store.createErr)
}

// ---------------------------------------------------------------------------
// Validate
// ---------------------------------------------------------------------------

func TestSessionValidateRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	s, err := m.CreateSession(ctx, "user-1", nil)
	require.NoError(t, err)

	got, err := m.ValidateSession(ctx, s.Token)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, s.ID, got.ID)
}

func TestSessionValidateExpired(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore(), WithDuration(-1*time.Hour))

	s, err := m.CreateSession(ctx, "user-1", nil)
	require.NoError(t, err)

	got, err := m.ValidateSession(ctx, s.Token)
	require.NoError(t, err)
	assert.Nil(t, got, "expired session should return nil")
}

func TestSessionValidateExpiredDeletesFromStore(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	m := NewSessionManager(store, WithDuration(-1*time.Hour))

	s, err := m.CreateSession(ctx, "user-1", nil)
	require.NoError(t, err)

	got, err := m.ValidateSession(ctx, s.Token)
	require.NoError(t, err)
	assert.Nil(t, got)

	// Verify the expired session was actually removed from the store.
	store.mu.Lock()
	_, exists := store.sessions[s.ID]
	store.mu.Unlock()
	assert.False(t, exists, "expired session should be deleted from store")
}

func TestSessionValidateNotFound(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	got, err := m.ValidateSession(ctx, "no-such-token")
	require.NoError(t, err)
	assert.Nil(t, got, "unknown token should return nil")
}

func TestSessionValidateStoreError(t *testing.T) {
	store := newMockStore()
	store.getByTokenErr = errors.New("read failure")
	m := NewSessionManager(store)

	_, err := m.ValidateSession(context.Background(), "any-token")
	require.Error(t, err)
	assert.ErrorIs(t, err, store.getByTokenErr)
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestSessionDelete(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	s, err := m.CreateSession(ctx, "user-1", nil)
	require.NoError(t, err)

	require.NoError(t, m.DeleteSession(ctx, s.ID))

	got, err := m.ValidateSession(ctx, s.Token)
	require.NoError(t, err)
	assert.Nil(t, got, "deleted session should not validate")
}

func TestSessionDeleteNonExistent(t *testing.T) {
	m := NewSessionManager(newMockStore())
	assert.NoError(t, m.DeleteSession(context.Background(), "does-not-exist"))
}

func TestSessionDeleteStoreError(t *testing.T) {
	store := newMockStore()
	store.deleteErr = errors.New("delete failure")
	m := NewSessionManager(store)

	err := m.DeleteSession(context.Background(), "any-id")
	require.Error(t, err)
	assert.ErrorIs(t, err, store.deleteErr)
}

// ---------------------------------------------------------------------------
// DeleteSubjectSessions
// ---------------------------------------------------------------------------

func TestSessionDeleteBySubject(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	s1, err := m.CreateSession(ctx, "user-1", nil)
	require.NoError(t, err)
	s2, err := m.CreateSession(ctx, "user-1", nil)
	require.NoError(t, err)

	require.NoError(t, m.DeleteSubjectSessions(ctx, "user-1"))

	for _, tok := range []string{s1.Token, s2.Token} {
		got, err := m.ValidateSession(ctx, tok)
		require.NoError(t, err)
		assert.Nil(t, got, "session should be gone after DeleteSubjectSessions")
	}
}

func TestSessionDeleteBySubjectNoSessions(t *testing.T) {
	m := NewSessionManager(newMockStore())
	assert.NoError(t, m.DeleteSubjectSessions(context.Background(), "no-such-user"))
}

func TestSessionDeleteSubjectStoreError(t *testing.T) {
	store := newMockStore()
	store.deleteBySubjectIDErr = errors.New("bulk delete failure")
	m := NewSessionManager(store)

	err := m.DeleteSubjectSessions(context.Background(), "user-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, store.deleteBySubjectIDErr)
}

// ---------------------------------------------------------------------------
// Uniqueness + concurrency
// ---------------------------------------------------------------------------

func TestSessionUniqueTokens(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	seen := make(map[string]struct{}, 10)
	for range 10 {
		s, err := m.CreateSession(ctx, "user-1", nil)
		require.NoError(t, err)
		assert.NotContains(t, seen, s.Token, "duplicate session token")
		seen[s.Token] = struct{}{}
	}
}

func TestSessionConcurrentCreateValidate(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	var wg sync.WaitGroup
	errs := make(chan error, 40)

	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := m.CreateSession(ctx, "user-1", nil)
			if err != nil {
				errs <- err
				return
			}
			got, err := m.ValidateSession(ctx, s.Token)
			if err != nil {
				errs <- err
				return
			}
			if got == nil {
				errs <- errors.New("expected session, got nil")
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}
