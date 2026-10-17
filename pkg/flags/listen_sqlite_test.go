package flags

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// sqliteStore is a read-only Store over the feature_flags table.
type sqliteStore struct{ db *sql.DB }

func (sqliteStore) Save(context.Context, Rule) error { return nil }

func (s sqliteStore) List(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, enabled, percent FROM feature_flags")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Rule
	for rows.Next() {
		r := Rule{Value: []byte("null")}
		if err := rows.Scan(&r.Key, &r.Enabled, &r.Percent); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func TestWithSQLite_ReloadsOnOtherConnectionWrite(t *testing.T) {
	// 2 pins the watcher's connection; 1 is hamr's sqlite default, where the
	// watcher must release its connection before reloading or List deadlocks.
	for _, conns := range []int{2, 1} {
		t.Run(fmt.Sprintf("maxOpenConns=%d", conns), func(t *testing.T) { testSQLiteReload(t, conns) })
	}
}

func testSQLiteReload(t *testing.T, conns int) {
	path := filepath.Join(t.TempDir(), "flags.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	app, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	app.SetMaxOpenConns(conns)
	other, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = app.Close(); _ = other.Close() })

	_, err = other.Exec(`CREATE TABLE feature_flags (
		key TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0,
		value TEXT NOT NULL DEFAULT 'null', subjects TEXT NOT NULL DEFAULT '[]',
		roles TEXT NOT NULL DEFAULT '[]', percent INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	c := New(sqliteStore{app}, WithSQLite(app), WithPollInterval(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, c.Start(ctx))

	time.Sleep(1500 * time.Millisecond) // let the watcher take its baseline
	_, err = other.Exec("INSERT INTO feature_flags (key, enabled, percent) VALUES ('a', 1, 100)")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		for _, r := range c.Rules() {
			if r.Key == "a" {
				return true
			}
		}
		return false
	}, 4*time.Second, 100*time.Millisecond)
}

func TestWatchPostgres_OneConnPoolDoesNotListen(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	done := make(chan struct{})
	go func() { watchPostgres(context.Background(), New(NewMemStore()), db); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchPostgres pinned the only connection instead of returning")
	}
}
