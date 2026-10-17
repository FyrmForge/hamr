//go:build integration

package flags

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const createTable = `CREATE TABLE feature_flags (
	key TEXT PRIMARY KEY,
	enabled BOOLEAN NOT NULL DEFAULT FALSE,
	value TEXT NOT NULL DEFAULT 'null',
	subjects TEXT NOT NULL DEFAULT '[]',
	roles TEXT NOT NULL DEFAULT '[]',
	percent INTEGER NOT NULL DEFAULT 0,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`

// sqlStore is a read-only Store over the feature_flags table.
type sqlStore struct{ db *sql.DB }

func (s sqlStore) Save(context.Context, Rule) error { return nil }

func (s sqlStore) List(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, enabled, value, percent FROM feature_flags")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Rule
	for rows.Next() {
		var r Rule
		var v string
		var en any
		if err := rows.Scan(&r.Key, &en, &v, &r.Percent); err != nil {
			return nil, err
		}
		r.Enabled = en == true || en == int64(1)
		r.Value = json.RawMessage(v)
		out = append(out, r)
	}
	return out, rows.Err()
}

func TestWithPostgres_ReloadsOnNotifyAndRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:18-alpine",
		tcpostgres.WithDatabase("hamr_flags_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		if dockerUnavailable(err) {
			t.Skipf("docker not available for integration test: %v", err)
		}
		require.NoError(t, err)
	}
	t.Cleanup(func() { _ = pgContainer.Terminate(context.Background()) })

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	appDB, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })
	adminDB, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = adminDB.Close() })

	_, err = adminDB.ExecContext(ctx, createTable)
	require.NoError(t, err)
	// Multi-statement through a plain Exec, and idempotent.
	for range 2 {
		_, err = adminDB.ExecContext(ctx, PostgresTriggerSQL)
		require.NoError(t, err)
	}

	c := New(sqlStore{appDB}, WithPostgres(appDB), WithPollInterval(time.Hour))
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, c.Start(runCtx))

	has := func(key string) bool {
		for _, r := range c.Rules() {
			if r.Key == key {
				return true
			}
		}
		return false
	}

	_, err = adminDB.ExecContext(ctx, "INSERT INTO feature_flags (key, enabled, percent) VALUES ('a', TRUE, 100)")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return has("a") }, 3*time.Second, 50*time.Millisecond)

	// Kill the listening backend; the watcher must reconnect and catch up.
	var killed int
	require.NoError(t, adminDB.QueryRowContext(ctx,
		"SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE query LIKE 'LISTEN %' AND pid <> pg_backend_pid()").Scan(&killed))
	require.Equal(t, 1, killed, "expected one listening backend")
	_, err = adminDB.ExecContext(ctx, "INSERT INTO feature_flags (key, enabled, percent) VALUES ('b', TRUE, 100)")
	require.NoError(t, err)
	assert.Eventually(t, func() bool { return has("b") }, 10*time.Second, 100*time.Millisecond)
}

func dockerUnavailable(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, sig := range []string{
		"cannot connect to the docker daemon",
		"docker daemon is not running",
		"is the docker daemon running",
		"error during connect",
		"connect: permission denied",
		"operation not permitted",
		"no such host",
	} {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}
