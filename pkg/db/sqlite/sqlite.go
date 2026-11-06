// Package sqlite provides SQLite connection management and schema migration
// utilities for hamr-scaffolded projects.
//
// It uses the pure-Go modernc.org/sqlite driver so projects compile without
// CGO. The package mirrors pkg/db's API shape (ConnectContext, Migrate, ...)
// with simpler semantics appropriate for a local-file database: no retries,
// no pool tuning. Defaults: WAL, foreign keys on, 5s busy timeout, in-memory
// temp store, 64 MB journal size limit and immediate transactions
// (_txlock=immediate). Any other pragma can be set with WithPragma, and
// PRAGMA optimize runs once after connecting.
package sqlite

import (
	"context"
	"fmt"
	"net/url"
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
	// TxLock sets the driver's _txlock DSN parameter (deferred, immediate or
	// exclusive). Default "immediate"; empty omits the parameter (driver
	// default, deferred).
	TxLock string
	// Pragmas are extra or overriding pragmas. Order only decides which value
	// wins for a duplicate name; the driver sets execution order (busy_timeout
	// first, then alphabetical). See WithPragma.
	Pragmas [][2]string
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

// WithTxLock sets the transaction locking mode (_txlock): "deferred",
// "immediate" or "exclusive". The default is "immediate", so a transaction
// takes the write lock at BEGIN instead of failing instantly with SQLITE_BUSY
// when two deferred transactions both try to upgrade to write. An empty string
// omits the parameter and uses the driver default (deferred).
func WithTxLock(mode string) ConnectOption {
	return func(c *ConnectConfig) { c.TxLock = mode }
}

// WithPragma sets any SQLite pragma on every pooled connection. If name is one
// hamr already sets (journal_mode, foreign_keys, busy_timeout, temp_store,
// journal_size_limit) the value replaces hamr's; otherwise it is appended.
// A later call for the same name wins. The driver runs pragmas in its own order
// (busy_timeout first, then alphabetical), so order-sensitive pragmas such as
// page_size will not take effect on a new WAL database. SQLite silently ignores
// unknown pragma names and invalid values; check with PRAGMA name if unsure.
// Examples:
//
//	sqlite.WithPragma("cache_size", "-20000") // negative = KiB, so ~20 MB
//	sqlite.WithPragma("synchronous", "NORMAL")
//
// synchronous=NORMAL in WAL mode is faster, but the last commits can be lost
// on power loss (the database itself cannot be corrupted).
func WithPragma(name, value string) ConnectOption {
	return func(c *ConnectConfig) { c.Pragmas = append(c.Pragmas, [2]string{name, value}) }
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
		TxLock:       "immediate",
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

	// Runs once at connect, not per pooled connection (modernc's
	// per-connection hook is process-global and cannot be removed).
	// Long-running apps can run `PRAGMA optimize` periodically themselves.
	// Best-effort: it is only a planner hint, and read-only databases
	// (mode=ro, immutable=1, query_only) reject it because it writes sqlite_stat1.
	_, _ = db.ExecContext(ctx, "PRAGMA optimize=0x10002")

	return db, nil
}

func isMemoryPath(path string) bool {
	return path == ":memory:" || path == "file::memory:" || path == ""
}

// buildDSN appends pragma query parameters to the file path so they are
// applied on every connection opened by database/sql's pool. If the caller's
// path already contains a query string (e.g. "file:x.db?mode=memory"), the
// pragma parameters are appended with "&" instead of "?". User pragmas from
// cfg.Pragmas replace hamr's value for the same name, else are appended.
func buildDSN(path string, cfg ConnectConfig) string {
	var pragmas [][2]string
	if cfg.JournalMode != "" {
		pragmas = append(pragmas, [2]string{"journal_mode", cfg.JournalMode})
	}
	fk := "off"
	if cfg.ForeignKeys {
		fk = "on"
	}
	pragmas = append(pragmas, [2]string{"foreign_keys", fk})
	if cfg.BusyTimeout > 0 {
		pragmas = append(pragmas, [2]string{"busy_timeout", fmt.Sprintf("%d", cfg.BusyTimeout.Milliseconds())})
	}
	pragmas = append(pragmas,
		[2]string{"temp_store", "MEMORY"},
		[2]string{"journal_size_limit", "67108864"},
	)

outer:
	for _, u := range cfg.Pragmas {
		for i, p := range pragmas {
			if strings.EqualFold(p[0], u[0]) {
				pragmas[i][1] = u[1]
				continue outer
			}
		}
		pragmas = append(pragmas, u)
	}

	params := ""
	add := func(kv string) {
		sep := "&"
		if params == "" && !strings.Contains(path, "?") {
			sep = "?"
		}
		params += sep + kv
	}
	for _, p := range pragmas {
		add("_pragma=" + p[0] + "(" + url.QueryEscape(p[1]) + ")")
	}
	if cfg.TxLock != "" {
		add("_txlock=" + cfg.TxLock)
	}

	return path + params
}
