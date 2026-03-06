// Package db provides PostgreSQL connection management with retry, keep-alive,
// and schema migration utilities.
package db

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// ConnectConfig holds connection pool and retry parameters.
type ConnectConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	MaxRetries      int
	AttemptTimeout  time.Duration
	PgBouncerSafe   bool
}

// ConnectOption configures a ConnectConfig.
type ConnectOption func(*ConnectConfig)

// WithMaxOpenConns sets the maximum number of open connections.
func WithMaxOpenConns(n int) ConnectOption {
	return func(c *ConnectConfig) { c.MaxOpenConns = n }
}

// WithMaxIdleConns sets the maximum number of idle connections.
func WithMaxIdleConns(n int) ConnectOption {
	return func(c *ConnectConfig) { c.MaxIdleConns = n }
}

// WithConnMaxIdleTime sets the maximum idle time for a connection.
func WithConnMaxIdleTime(d time.Duration) ConnectOption {
	return func(c *ConnectConfig) { c.ConnMaxIdleTime = d }
}

// WithConnMaxLifetime sets the maximum lifetime of a connection.
func WithConnMaxLifetime(d time.Duration) ConnectOption {
	return func(c *ConnectConfig) { c.ConnMaxLifetime = d }
}

// WithMaxRetries sets the number of connection retry attempts.
func WithMaxRetries(n int) ConnectOption {
	return func(c *ConnectConfig) { c.MaxRetries = n }
}

// WithAttemptTimeout sets the timeout for each connectivity check attempt.
func WithAttemptTimeout(d time.Duration) ConnectOption {
	return func(c *ConnectConfig) { c.AttemptTimeout = d }
}

// WithPgBouncerSafe forces pgx simple protocol mode, which is compatible with
// PgBouncer transaction pooling.
func WithPgBouncerSafe(enabled bool) ConnectOption {
	return func(c *ConnectConfig) { c.PgBouncerSafe = enabled }
}

// Connect opens a PostgreSQL connection using context.Background().
// Use ConnectContext to control startup cancellation.
func Connect(databaseURL string, opts ...ConnectOption) (*sqlx.DB, error) {
	return ConnectContext(context.Background(), databaseURL, opts...)
}

// ConnectContext opens a PostgreSQL connection with retry and exponential
// backoff + jitter. On success it configures pool parameters and validates
// connectivity via PingContext.
func ConnectContext(ctx context.Context, databaseURL string, opts ...ConnectOption) (*sqlx.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg := ConnectConfig{
		MaxOpenConns:    10,
		MaxIdleConns:    10,
		ConnMaxIdleTime: 5 * time.Minute,
		ConnMaxLifetime: 30 * time.Minute,
		MaxRetries:      5,
		AttemptTimeout:  3 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.MaxOpenConns < 0 {
		return nil, fmt.Errorf("db: max open conns must be >= 0, got %d", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns < 0 {
		return nil, fmt.Errorf("db: max idle conns must be >= 0, got %d", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxIdleTime < 0 {
		return nil, fmt.Errorf("db: conn max idle time must be >= 0, got %v", cfg.ConnMaxIdleTime)
	}
	if cfg.ConnMaxLifetime < 0 {
		return nil, fmt.Errorf("db: conn max lifetime must be >= 0, got %v", cfg.ConnMaxLifetime)
	}
	if cfg.MaxRetries < 1 {
		return nil, fmt.Errorf("db: max retries must be >= 1, got %d", cfg.MaxRetries)
	}
	if cfg.AttemptTimeout <= 0 {
		return nil, fmt.Errorf("db: attempt timeout must be positive, got %v", cfg.AttemptTimeout)
	}

	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	if cfg.PgBouncerSafe {
		connConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}

	stdDB := stdlib.OpenDB(*connConfig)
	db := sqlx.NewDb(stdDB, "pgx")

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	var lastErr error
	for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, cfg.AttemptTimeout)
		lastErr = db.PingContext(pingCtx)
		cancel()
		if lastErr == nil {
			return db, nil
		}

		if attempt == cfg.MaxRetries-1 {
			break
		}

		sleep := backoffWithJitter(attempt)
		select {
		case <-time.After(sleep):
		case <-ctx.Done():
			_ = db.Close()
			return nil, fmt.Errorf("db: connect canceled: %w", ctx.Err())
		}
	}

	_ = db.Close()
	return nil, fmt.Errorf("db: connecting: %w", lastErr)
}

func backoffWithJitter(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 5 {
		attempt = 5 // cap at 32s before jitter
	}
	base := time.Duration(1<<attempt) * time.Second
	return jitter(base)
}

func jitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}

	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return base
	}

	// Scale by 0.5x to 1.5x to avoid synchronized retries across instances.
	f := 0.5 + (float64(binary.LittleEndian.Uint64(b[:])%1000) / 1000.0)
	return time.Duration(float64(base) * f)
}

// KeepAliveConfig configures optional background connectivity checks.
type KeepAliveConfig struct {
	Interval time.Duration
	Timeout  time.Duration
}

// StartKeepAlive launches an optional keep-alive loop. For backward
// compatibility, poolSize is ignored; only one goroutine is started.
func StartKeepAlive(ctx context.Context, db *sqlx.DB, interval time.Duration, _ int) {
	StartKeepAliveWithConfig(ctx, db, KeepAliveConfig{
		Interval: interval,
		Timeout:  3 * time.Second,
	})
}

// StartKeepAliveWithConfig launches one goroutine that periodically runs
// PingContext with a timeout. This is intended for explicit opt-in usage.
func StartKeepAliveWithConfig(ctx context.Context, db *sqlx.DB, cfg KeepAliveConfig) {
	if ctx == nil || db == nil || cfg.Interval <= 0 || cfg.Timeout <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
				err := db.PingContext(pingCtx)
				cancel()
				if err != nil {
					slog.Default().Error("db: keep-alive ping failed", "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
