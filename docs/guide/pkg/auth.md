# Auth — Password Hashing & Session Management

`hamr/pkg/auth` provides Argon2id password hashing, cryptographically-secure token
generation, and a pluggable session manager with functional options.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/auth"
```

## Password Hashing

Uses Argon2id with production-safe defaults (3 iterations, 64 MB memory, 2 threads).

### Hash a password

```go
hash, err := auth.HashPassword("s3cret!")
// hash is a PHC-format string: $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
```

### Verify a password

```go
match, err := auth.CheckPassword("s3cret!", hash)
if !match {
    // wrong password
}
```

### Custom parameters

```go
hash, err := auth.HashPasswordWithConfig("s3cret!", auth.HashConfig{
    Time:        4,
    Memory:      128 * 1024,
    Parallelism: 4,
    KeyLength:   32,
    SaltLength:  16,
})
```

## Token Generation

Cryptographically-secure random tokens encoded with base64 raw-URL encoding.

```go
token, err := auth.GenerateToken()         // 32 bytes
token, err := auth.GenerateTokenN(64)      // 64 bytes
```

## Session Management

`SessionManager` handles session lifecycle on top of a pluggable `SessionStore`.

### SessionStore Interface

Projects implement this for their database:

```go
type SessionStore interface {
    Create(ctx context.Context, s *Session) error
    GetByToken(ctx context.Context, token string) (*Session, error)
    Delete(ctx context.Context, id string) error
    DeleteBySubjectID(ctx context.Context, subjectID string) error
}
```

`GetByToken` returns `(nil, nil)` when not found.

### Session Struct

```go
type Session struct {
    ID        string
    SubjectID string         // empty for anonymous sessions
    Token     string
    ExpiresAt time.Time
    CreatedAt time.Time
    Metadata  map[string]any
}
```

`SubjectID` is always a `string` — projects convert their native ID type at the
boundary.

### Creating a SessionManager

```go
sm := auth.NewSessionManager(store,
    auth.WithDuration(24*time.Hour),
    auth.WithCookieName("session"),
    auth.WithCookieSecure(true),
    auth.WithSameSite(http.SameSiteLaxMode),
)
```

Defaults: 7-day duration, cookie name `session_token`, path `/`, Secure true,
SameSite Lax.

### Session Operations

```go
// Create
session, err := sm.CreateSession(ctx, userID, map[string]any{"ip": clientIP})

// Validate (deletes expired sessions automatically)
session, err := sm.ValidateSession(ctx, token)
if session == nil {
    // not found or expired
}

// Delete
err := sm.DeleteSession(ctx, sessionID)

// Delete all sessions for a user (e.g. password change)
err := sm.DeleteSubjectSessions(ctx, userID)
```

### Cookie Configuration

Read cookie settings to build HTTP cookies in your handlers:

```go
http.Cookie{
    Name:     sm.CookieName(),
    Value:    session.Token,
    Path:     sm.CookiePath(),
    Secure:   sm.CookieSecure(),
    SameSite: sm.SameSite(),
    Expires:  session.ExpiresAt,
    HttpOnly: true,
}
```

## API Reference

```go
// Password hashing
var DefaultHashConfig HashConfig
func HashPassword(password string) (string, error)
func HashPasswordWithConfig(password string, cfg HashConfig) (string, error)
func CheckPassword(password, encodedHash string) (bool, error)

// Token generation
func GenerateToken() (string, error)
func GenerateTokenN(n int) (string, error)

// Session management
func NewSessionManager(store SessionStore, opts ...SessionOption) *SessionManager
func (m *SessionManager) CreateSession(ctx context.Context, subjectID string, metadata map[string]any) (*Session, error)
func (m *SessionManager) ValidateSession(ctx context.Context, token string) (*Session, error)
func (m *SessionManager) DeleteSession(ctx context.Context, id string) error
func (m *SessionManager) DeleteSubjectSessions(ctx context.Context, subjectID string) error
func (m *SessionManager) CookieName() string
func (m *SessionManager) CookiePath() string
func (m *SessionManager) CookieSecure() bool
func (m *SessionManager) SameSite() http.SameSite
func (m *SessionManager) Duration() time.Duration

// Session options
func WithDuration(d time.Duration) SessionOption
func WithCookieName(name string) SessionOption
func WithCookiePath(path string) SessionOption
func WithCookieSecure(secure bool) SessionOption
func WithSameSite(ss http.SameSite) SessionOption
```
