package middleware

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/labstack/echo/v4"
)

// DB is the minimal database interface satisfied by *sql.DB and *sqlx.DB.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RateLimitStore checks whether a key is within its rate limit.
type RateLimitStore interface {
	Allow(ctx context.Context, key string, rate int, window time.Duration) (allowed bool, remaining int, resetAt time.Time, err error)
}

// RateLimitConfig configures rate limiting middleware.
type RateLimitConfig struct {
	Store   RateLimitStore
	Rate    int                                  // max requests per window (default: 60)
	Burst   int                                  // additional burst allowance over Rate (default: 10)
	Window  time.Duration                        // sliding window duration (default: 1 minute)
	KeyFunc func(c echo.Context) (string, error) // default: c.RealIP()
}

// RateLimit returns rate limiting middleware using the given store with
// default settings (60 req/min + 10 burst).
func RateLimit(store RateLimitStore) echo.MiddlewareFunc {
	return RateLimitWithConfig(RateLimitConfig{
		Store: store,
		Rate:  60,
		Burst: 10,
	})
}

// RateLimitWithConfig returns rate limiting middleware with the given config.
func RateLimitWithConfig(cfg RateLimitConfig) echo.MiddlewareFunc {
	if cfg.Rate <= 0 {
		cfg.Rate = 60
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = func(c echo.Context) (string, error) {
			return c.RealIP(), nil
		}
	}

	rate := cfg.Rate + cfg.Burst
	window := cfg.Window

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key, err := cfg.KeyFunc(c)
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "rate limit key error")
			}

			allowed, remaining, resetAt, err := cfg.Store.Allow(c.Request().Context(), key, rate, window)
			if err != nil {
				logger := logging.FromContext(c.Request().Context())
				logger.Error("rate limit store error", slog.String("error", err.Error()))
				// Fail open — allow request on store error.
				return next(c)
			}

			c.Response().Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			c.Response().Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

			if !allowed {
				retryAfter := time.Until(resetAt).Seconds()
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Response().Header().Set("Retry-After", strconv.Itoa(int(retryAfter)))

				logger := logging.FromContext(c.Request().Context())
				logger.Warn("rate limit exceeded",
					slog.String("key", key),
					slog.Int("rate", rate),
				)

				return echo.NewHTTPError(http.StatusTooManyRequests, "rate limit exceeded")
			}

			return next(c)
		}
	}
}

// ---------------------------------------------------------------------------
// Memory Store
// ---------------------------------------------------------------------------

type window struct {
	count int
	start time.Time
}

// MemoryStore is an in-memory fixed-window rate limit store.
type MemoryStore struct {
	mu              sync.Mutex
	windows         map[string]*window
	maxSize         int
	insertOrder     []string
	cleanupCtx      context.Context
	cleanupInterval time.Duration
	window          time.Duration
}

// MemoryStoreOption configures a MemoryStore.
type MemoryStoreOption func(*MemoryStore)

// WithMaxSize sets the maximum number of entries in the memory store.
// When full, the oldest entry is evicted.
func WithMaxSize(n int) MemoryStoreOption {
	return func(s *MemoryStore) { s.maxSize = n }
}

// WithAutoCleanup starts a background goroutine that calls CleanupExpired
// at the given interval. The goroutine stops when ctx is cancelled.
func WithAutoCleanup(ctx context.Context, interval time.Duration) MemoryStoreOption {
	return func(s *MemoryStore) {
		s.cleanupCtx = ctx
		s.cleanupInterval = interval
	}
}

// WithWindow sets the rate limit window duration used by autoCleanup when
// expiring entries. If unset, autoCleanup falls back to cleanupInterval.
func WithWindow(d time.Duration) MemoryStoreOption {
	return func(s *MemoryStore) { s.window = d }
}

// NewMemoryStore returns a new in-memory rate limit store.
func NewMemoryStore(opts ...MemoryStoreOption) *MemoryStore {
	s := &MemoryStore{
		windows: make(map[string]*window),
	}
	for _, o := range opts {
		o(s)
	}
	if s.cleanupCtx != nil && s.cleanupInterval > 0 {
		go s.autoCleanup()
	}
	return s
}

// Allow implements RateLimitStore.
func (s *MemoryStore) Allow(_ context.Context, key string, rate int, dur time.Duration) (bool, int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	w, ok := s.windows[key]
	if !ok || now.Sub(w.start) >= dur {
		isNew := !ok
		w = &window{count: 0, start: now}
		s.windows[key] = w

		if isNew {
			// Evict oldest entry if max size exceeded.
			if s.maxSize > 0 && len(s.insertOrder) >= s.maxSize {
				oldest := s.insertOrder[0]
				s.insertOrder = s.insertOrder[1:]
				delete(s.windows, oldest)
			}
			s.insertOrder = append(s.insertOrder, key)
		}
	}

	w.count++
	resetAt := w.start.Add(dur)
	remaining := rate - w.count
	if remaining < 0 {
		remaining = 0
	}

	return w.count <= rate, remaining, resetAt, nil
}

// CleanupExpired removes expired windows.
func (s *MemoryStore) CleanupExpired(window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for key, w := range s.windows {
		if now.Sub(w.start) >= window {
			delete(s.windows, key)
		}
	}

	// Rebuild insertOrder to remove deleted keys.
	if s.maxSize > 0 {
		alive := s.insertOrder[:0]
		for _, key := range s.insertOrder {
			if _, ok := s.windows[key]; ok {
				alive = append(alive, key)
			}
		}
		s.insertOrder = alive
	}
}

func (s *MemoryStore) autoCleanup() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	expiry := s.window
	if expiry == 0 {
		expiry = s.cleanupInterval
	}

	for {
		select {
		case <-s.cleanupCtx.Done():
			return
		case <-ticker.C:
			s.CleanupExpired(expiry)
		}
	}
}

// ---------------------------------------------------------------------------
// PG Store
// ---------------------------------------------------------------------------

// PGStore is a PostgreSQL-backed fixed-window rate limit store using an
// UNLOGGED table for performance.
type PGStore struct {
	db DB
}

// NewPGStore returns a new PostgreSQL rate limit store.
func NewPGStore(db DB) *PGStore {
	return &PGStore{db: db}
}

// CreateTable creates the _rate_limits table if it does not exist.
func (s *PGStore) CreateTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE UNLOGGED TABLE IF NOT EXISTS _rate_limits (
			key          TEXT        NOT NULL PRIMARY KEY,
			hit_count    INTEGER     NOT NULL DEFAULT 1,
			window_start TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

// Allow implements RateLimitStore using an atomic upsert.
func (s *PGStore) Allow(ctx context.Context, key string, rate int, dur time.Duration) (bool, int, time.Time, error) {
	secs := dur.Seconds()

	var hitCount int
	var resetAt time.Time
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO _rate_limits (key, hit_count, window_start)
		VALUES ($1, 1, now())
		ON CONFLICT (key) DO UPDATE SET
			hit_count = CASE
				WHEN _rate_limits.window_start + make_interval(secs => $2) <= now() THEN 1
				ELSE _rate_limits.hit_count + 1
			END,
			window_start = CASE
				WHEN _rate_limits.window_start + make_interval(secs => $2) <= now() THEN now()
				ELSE _rate_limits.window_start
			END
		RETURNING hit_count, window_start + make_interval(secs => $2) AS reset_at`,
		key, secs,
	).Scan(&hitCount, &resetAt)
	if err != nil {
		return false, 0, time.Time{}, fmt.Errorf("rate limit: %w", err)
	}

	remaining := rate - hitCount
	if remaining < 0 {
		remaining = 0
	}

	return hitCount <= rate, remaining, resetAt, nil
}

// Cleanup removes expired rate limit entries.
func (s *PGStore) Cleanup(ctx context.Context, dur time.Duration) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM _rate_limits WHERE window_start < now() - $1::interval",
		fmt.Sprintf("%d seconds", int(dur.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
