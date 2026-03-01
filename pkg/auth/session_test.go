package auth

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mockStore is an in-memory SessionStore for testing.
type mockStore struct {
	mu       sync.Mutex
	sessions map[string]*Session // keyed by ID
}

func newMockStore() *mockStore {
	return &mockStore{sessions: make(map[string]*Session)}
}

func (s *mockStore) Create(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.ID] = sess
	return nil
}

func (s *mockStore) GetByToken(_ context.Context, token string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	delete(s.sessions, id)
	return nil
}

func (s *mockStore) DeleteBySubjectID(_ context.Context, subjectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.SubjectID == subjectID {
			delete(s.sessions, id)
		}
	}
	return nil
}

func TestSessionManagerDefaults(t *testing.T) {
	m := NewSessionManager(newMockStore())

	if m.Duration() != 7*24*time.Hour {
		t.Fatalf("expected 7d duration, got %v", m.Duration())
	}
	if m.CookieName() != "session_token" {
		t.Fatalf("expected session_token, got %s", m.CookieName())
	}
	if m.CookiePath() != "/" {
		t.Fatalf("expected /, got %s", m.CookiePath())
	}
	if !m.CookieSecure() {
		t.Fatal("expected secure=true")
	}
	if m.SameSite() != http.SameSiteLaxMode {
		t.Fatalf("expected Lax, got %v", m.SameSite())
	}
}

func TestSessionManagerOptions(t *testing.T) {
	m := NewSessionManager(newMockStore(),
		WithDuration(1*time.Hour),
		WithCookieName("my_sess"),
		WithCookiePath("/app"),
		WithCookieSecure(false),
		WithSameSite(http.SameSiteStrictMode),
	)

	if m.Duration() != 1*time.Hour {
		t.Fatalf("expected 1h, got %v", m.Duration())
	}
	if m.CookieName() != "my_sess" {
		t.Fatalf("expected my_sess, got %s", m.CookieName())
	}
	if m.CookiePath() != "/app" {
		t.Fatalf("expected /app, got %s", m.CookiePath())
	}
	if m.CookieSecure() {
		t.Fatal("expected secure=false")
	}
	if m.SameSite() != http.SameSiteStrictMode {
		t.Fatalf("expected Strict, got %v", m.SameSite())
	}
}

func TestSessionCreate(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	s, err := m.CreateSession(ctx, "user-42", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := uuid.Parse(s.ID); err != nil {
		t.Fatalf("expected valid UUID, got %q: %v", s.ID, err)
	}
	if s.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if s.SubjectID != "user-42" {
		t.Fatalf("expected user-42, got %s", s.SubjectID)
	}
	if !s.ExpiresAt.After(time.Now()) {
		t.Fatal("expected future ExpiresAt")
	}
}

func TestSessionValidateRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	s, err := m.CreateSession(ctx, "user-1", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := m.ValidateSession(ctx, s.Token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if got == nil || got.ID != s.ID {
		t.Fatalf("expected session %s, got %v", s.ID, got)
	}
}

func TestSessionValidateExpired(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore(), WithDuration(-1*time.Hour))

	s, err := m.CreateSession(ctx, "user-1", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := m.ValidateSession(ctx, s.Token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for expired session")
	}
}

func TestSessionValidateNotFound(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	got, err := m.ValidateSession(ctx, "no-such-token")
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for unknown token")
	}
}

func TestSessionDelete(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	s, err := m.CreateSession(ctx, "user-1", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := m.DeleteSession(ctx, s.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	got, err := m.ValidateSession(ctx, s.Token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestSessionDeleteBySubject(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	m := NewSessionManager(store)

	s1, err := m.CreateSession(ctx, "user-1", nil)
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	s2, err := m.CreateSession(ctx, "user-1", nil)
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}

	if err := m.DeleteSubjectSessions(ctx, "user-1"); err != nil {
		t.Fatalf("DeleteSubjectSessions: %v", err)
	}

	for _, tok := range []string{s1.Token, s2.Token} {
		got, err := m.ValidateSession(ctx, tok)
		if err != nil {
			t.Fatalf("ValidateSession: %v", err)
		}
		if got != nil {
			t.Fatal("expected nil after delete by subject")
		}
	}
}

func TestSessionUniqueTokens(t *testing.T) {
	ctx := context.Background()
	m := NewSessionManager(newMockStore())

	seen := make(map[string]struct{}, 10)
	for range 10 {
		s, err := m.CreateSession(ctx, "user-1", nil)
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if _, ok := seen[s.Token]; ok {
			t.Fatal("duplicate session token")
		}
		seen[s.Token] = struct{}{}
	}
}
