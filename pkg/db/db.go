// Package db provides PostgreSQL connection management with retry, keep-alive,
// and schema migration utilities.
package db

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

// ConnectConfig holds connection pool and retry parameters.
type ConnectConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	MaxRetries      int
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

// Connect opens a PostgreSQL connection using the pgx driver with retry and
// backoff. On success it configures pool parameters and pings the database.
func Connect(databaseURL string, opts ...ConnectOption) (*sqlx.DB, error) {
	cfg := ConnectConfig{
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxIdleTime: 5 * time.Second,
		ConnMaxLifetime: 1 * time.Minute,
		MaxRetries:      3,
	}
	for _, o := range opts {
		o(&cfg)
	}

	var db *sqlx.DB
	var err error

	for attempt := range cfg.MaxRetries {
		db, err = sqlx.Open("pgx", databaseURL)
		if err == nil {
			err = db.Ping()
		}
		if err == nil {
			break
		}

		if db != nil {
			_ = db.Close()
		}

		if attempt < cfg.MaxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("db: connecting: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return db, nil
}

// StartKeepAlive launches poolSize goroutines that each ping db every interval.
// Failures are logged via slog.Default(). The goroutines run for the lifetime
// of the process.
func StartKeepAlive(db *sqlx.DB, interval time.Duration, poolSize int) {
	for range poolSize {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				if err := db.Ping(); err != nil {
					slog.Default().Error("db: keep-alive ping failed", "error", err)
				}
			}
		}()
	}
}
