package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Session represents a user session.
type Session struct {
	ID        string
	SubjectID string
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
	Metadata  map[string]any
}

// SessionStore is the persistence interface for sessions.
// GetByToken returns (nil, nil) when the token is not found.
type SessionStore interface {
	Create(ctx context.Context, s *Session) error
	GetByToken(ctx context.Context, token string) (*Session, error)
	Delete(ctx context.Context, id string) error
	DeleteBySubjectID(ctx context.Context, subjectID string) error
}

// SessionToucher is optionally implemented by a SessionStore that supports
// extending session expiry without creating a new session.
type SessionToucher interface {
	Touch(ctx context.Context, id string, newExpiresAt time.Time) error
}

// SessionManager manages session lifecycle on top of a SessionStore.
type SessionManager struct {
	store             SessionStore
	duration          time.Duration
	slidingThreshold  time.Duration
	cookieName        string
	cookiePath        string
	cookieSecure      bool
	sameSite          http.SameSite
}

// SessionOption configures a SessionManager.
type SessionOption func(*SessionManager)

// WithDuration sets the session lifetime.
func WithDuration(d time.Duration) SessionOption {
	return func(m *SessionManager) { m.duration = d }
}

// WithCookieName sets the session cookie name.
func WithCookieName(name string) SessionOption {
	return func(m *SessionManager) { m.cookieName = name }
}

// WithCookiePath sets the session cookie path.
func WithCookiePath(path string) SessionOption {
	return func(m *SessionManager) { m.cookiePath = path }
}

// WithCookieSecure sets the Secure flag on the session cookie.
func WithCookieSecure(secure bool) SessionOption {
	return func(m *SessionManager) { m.cookieSecure = secure }
}

// WithSameSite sets the SameSite attribute on the session cookie.
func WithSameSite(ss http.SameSite) SessionOption {
	return func(m *SessionManager) { m.sameSite = ss }
}

// WithSlidingRefresh sets the threshold after which a validated session's
// expiry is automatically extended. When the session age exceeds threshold,
// the store is type-asserted to SessionToucher and, if implemented, Touch is
// called to push the expiry forward by the configured duration.
func WithSlidingRefresh(threshold time.Duration) SessionOption {
	return func(m *SessionManager) { m.slidingThreshold = threshold }
}

// NewSessionManager returns a SessionManager with sensible defaults.
func NewSessionManager(store SessionStore, opts ...SessionOption) *SessionManager {
	m := &SessionManager{
		store:        store,
		duration:     7 * 24 * time.Hour,
		cookieName:   "session_token",
		cookiePath:   "/",
		cookieSecure: true,
		sameSite:     http.SameSiteLaxMode,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// CreateSession creates a new session for the given subject.
func (m *SessionManager) CreateSession(ctx context.Context, subjectID string, metadata map[string]any) (*Session, error) {
	token, err := GenerateToken()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	s := &Session{
		ID:        uuid.New().String(),
		SubjectID: subjectID,
		Token:     token,
		ExpiresAt: now.Add(m.duration),
		CreatedAt: now,
		Metadata:  metadata,
	}

	if err := m.store.Create(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// ValidateSession looks up a session by token and checks its expiry.
// Expired sessions are deleted. Returns (nil, nil) for expired or not-found.
func (m *SessionManager) ValidateSession(ctx context.Context, token string) (*Session, error) {
	s, err := m.store.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}

	if time.Now().After(s.ExpiresAt) {
		_ = m.store.Delete(ctx, s.ID)
		return nil, nil
	}

	// Sliding refresh: extend expiry when session age exceeds the threshold.
	if m.slidingThreshold > 0 && time.Since(s.CreatedAt) > m.slidingThreshold {
		if toucher, ok := m.store.(SessionToucher); ok {
			if err := toucher.Touch(ctx, s.ID, time.Now().Add(m.duration)); err != nil {
				slog.Default().Warn("auth: sliding refresh failed", "session", s.ID, "error", err)
			}
		}
	}

	return s, nil
}

// DeleteSession removes a session by ID.
func (m *SessionManager) DeleteSession(ctx context.Context, id string) error {
	return m.store.Delete(ctx, id)
}

// DeleteSubjectSessions removes all sessions for a subject.
func (m *SessionManager) DeleteSubjectSessions(ctx context.Context, subjectID string) error {
	return m.store.DeleteBySubjectID(ctx, subjectID)
}

// CookieName returns the configured cookie name.
func (m *SessionManager) CookieName() string { return m.cookieName }

// CookiePath returns the configured cookie path.
func (m *SessionManager) CookiePath() string { return m.cookiePath }

// CookieSecure returns the configured Secure flag.
func (m *SessionManager) CookieSecure() bool { return m.cookieSecure }

// SameSite returns the configured SameSite attribute.
func (m *SessionManager) SameSite() http.SameSite { return m.sameSite }

// Duration returns the configured session duration.
func (m *SessionManager) Duration() time.Duration { return m.duration }
