// Package sqlite provides SQLite connection management and schema migration
// utilities for hamr-scaffolded projects.
//
// It uses the pure-Go modernc.org/sqlite driver so projects compile without
// CGO. The package mirrors pkg/db's API shape (ConnectContext, Migrate, ...)
// with simpler semantics appropriate for a local-file database: no retries,
// no pool tuning, WAL + foreign-keys + busy-timeout pragmas by default.
package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	// Register the modernc.org/sqlite driver under the name "sqlite".
	_ "modernc.org/sqlite"
)

// ConnectConfig holds connection parameters.
type ConnectConfig struct {
	// JournalMode sets PRAGMA journal_mode. Default "WAL".
	JournalMode string
	// ForeignKeys toggles PRAGMA foreign_keys. Default true.
	ForeignKeys bool
	// BusyTimeout sets PRAGMA busy_timeout. Default 5s.
	BusyTimeout time.Duration
	// MaxOpenConns caps the connection pool. Default 1 — SQLite is
	// single-writer, so higher values only help read-heavy workloads on WAL.
	MaxOpenConns int
}

// ConnectOption configures a ConnectConfig.
type ConnectOption func(*ConnectConfig)

// WithJournalMode overrides PRAGMA journal_mode.
func WithJournalMode(mode string) ConnectOption {
	return func(c *ConnectConfig) { c.JournalMode = mode }
}

// WithForeignKeys toggles PRAGMA foreign_keys.
func WithForeignKeys(enabled bool) ConnectOption {
	return func(c *ConnectConfig) { c.ForeignKeys = enabled }
}

// WithBusyTimeout overrides PRAGMA busy_timeout.
func WithBusyTimeout(d time.Duration) ConnectOption {
	return func(c *ConnectConfig) { c.BusyTimeout = d }
}

// WithMaxOpenConns overrides the connection pool cap.
func WithMaxOpenConns(n int) ConnectOption {
	return func(c *ConnectConfig) { c.MaxOpenConns = n }
}

// Connect opens a SQLite database file using context.Background().
func Connect(path string, opts ...ConnectOption) (*sqlx.DB, error) {
	return ConnectContext(context.Background(), path, opts...)
}

// ConnectContext opens a SQLite database file, ensuring the parent directory
// exists and applying the configured pragmas on every connection. It then
// validates connectivity via PingContext.
func ConnectContext(ctx context.Context, path string, opts ...ConnectOption) (*sqlx.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if path == "" {
		return nil, fmt.Errorf("sqlite: database path must not be empty")
	}

	cfg := ConnectConfig{
		JournalMode:  "WAL",
		ForeignKeys:  true,
		BusyTimeout:  5 * time.Second,
		MaxOpenConns: 1,
	}
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.BusyTimeout < 0 {
		return nil, fmt.Errorf("sqlite: busy timeout must be >= 0, got %v", cfg.BusyTimeout)
	}
	if cfg.MaxOpenConns < 0 {
		return nil, fmt.Errorf("sqlite: max open conns must be >= 0, got %d", cfg.MaxOpenConns)
	}

	if !isMemoryPath(path) {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("sqlite: ensuring parent directory: %w", err)
			}
		}
	}

	dsn := buildDSN(path, cfg)
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	return db, nil
}

func isMemoryPath(path string) bool {
	return path == ":memory:" || path == "file::memory:" || path == ""
}

// buildDSN appends pragma query parameters to the file path so they are
// applied on every connection opened by database/sql's pool. If the caller's
// path already contains a query string (e.g. "file:x.db?mode=memory"), the
// pragma parameters are appended with "&" instead of "?".
func buildDSN(path string, cfg ConnectConfig) string {
	first := !strings.Contains(path, "?")
	params := ""
	appendParam := func(key, value string) {
		sep := "&"
		if first && params == "" {
			sep = "?"
		}
		params += sep + "_pragma=" + key + "(" + value + ")"
	}

	if cfg.JournalMode != "" {
		appendParam("journal_mode", cfg.JournalMode)
	}
	if cfg.ForeignKeys {
		appendParam("foreign_keys", "on")
	} else {
		appendParam("foreign_keys", "off")
	}
	if cfg.BusyTimeout > 0 {
		ms := cfg.BusyTimeout.Milliseconds()
		appendParam("busy_timeout", fmt.Sprintf("%d", ms))
	}

	return path + params
}
