package sqlite

import (
	"context"
	"embed"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

func TestConnect_EmptyPath(t *testing.T) {
	_, err := Connect("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path must not be empty")
}

func TestConnect_NegativeBusyTimeout(t *testing.T) {
	_, err := Connect(":memory:", WithBusyTimeout(-1*time.Second))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "busy timeout")
}

func TestConnect_NegativeMaxOpenConns(t *testing.T) {
	_, err := Connect(":memory:", WithMaxOpenConns(-1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max open conns")
}

func TestConnect_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", "test.db")

	db, err := Connect(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Verify pragmas applied.
	var fk int
	require.NoError(t, db.Get(&fk, "PRAGMA foreign_keys"))
	assert.Equal(t, 1, fk, "foreign_keys pragma should be on")

	var mode string
	require.NoError(t, db.Get(&mode, "PRAGMA journal_mode"))
	assert.Equal(t, "wal", mode)
}

func TestConnect_ForeignKeysOff(t *testing.T) {
	db, err := Connect(":memory:", WithForeignKeys(false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var fk int
	require.NoError(t, db.Get(&fk, "PRAGMA foreign_keys"))
	assert.Equal(t, 0, fk)
}

func TestConnect_CustomJournalMode(t *testing.T) {
	db, err := Connect(":memory:", WithJournalMode("MEMORY"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var mode string
	require.NoError(t, db.Get(&mode, "PRAGMA journal_mode"))
	assert.Equal(t, "memory", mode)
}

func TestConnect_PreservesExistingQueryParams(t *testing.T) {
	// Callers may pass a URI-style DSN with query params already set (e.g.
	// `file:...?cache=shared`). Our pragma appender must not corrupt it by
	// duplicating "?" separators.
	db, err := Connect("file::memory:?cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var fk int
	require.NoError(t, db.Get(&fk, "PRAGMA foreign_keys"))
	assert.Equal(t, 1, fk)
}

func TestBuildDSN_AppendsWithAmpersandWhenQueryPresent(t *testing.T) {
	cfg := ConnectConfig{JournalMode: "WAL", ForeignKeys: true, BusyTimeout: 5 * time.Second}
	dsn := buildDSN("file:test.db?mode=memory", cfg)
	// Must not emit "?mode=memory?_pragma=...".
	assert.NotContains(t, dsn, "?_pragma=")
	assert.Contains(t, dsn, "?mode=memory&_pragma=journal_mode(WAL)")
}

func TestBuildDSN_UsesQuestionMarkWhenNoQuery(t *testing.T) {
	cfg := ConnectConfig{JournalMode: "WAL", ForeignKeys: true, BusyTimeout: 5 * time.Second}
	dsn := buildDSN("./app.db", cfg)
	assert.Equal(t, "./app.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)"+
		"&_pragma=temp_store(MEMORY)&_pragma=journal_size_limit(67108864)", dsn)
}

func TestBuildDSN_TxLock(t *testing.T) {
	cfg := ConnectConfig{ForeignKeys: true, TxLock: "immediate"}
	assert.Contains(t, buildDSN("./app.db", cfg), "&_txlock=immediate")

	cfg.TxLock = ""
	assert.NotContains(t, buildDSN("./app.db", cfg), "_txlock")

	cfg.TxLock = "deferred"
	assert.Contains(t, buildDSN("./app.db", cfg), "_txlock=deferred")
}

func TestWithTxLock(t *testing.T) {
	cfg := ConnectConfig{TxLock: "immediate"}
	WithTxLock("")(&cfg)
	assert.Equal(t, "", cfg.TxLock)
	WithTxLock("deferred")(&cfg)
	assert.Equal(t, "deferred", cfg.TxLock)
}

func TestBuildDSN_WithPragma(t *testing.T) {
	cfg := ConnectConfig{JournalMode: "WAL", ForeignKeys: true}
	WithPragma("temp_store", "FILE")(&cfg)
	WithPragma("cache_size", "-1000")(&cfg)
	WithPragma("cache_size", "-20000")(&cfg)
	dsn := buildDSN("./app.db", cfg)

	assert.Equal(t, 1, strings.Count(dsn, "_pragma=temp_store("))
	assert.Contains(t, dsn, "_pragma=temp_store(FILE)")
	assert.NotContains(t, dsn, "temp_store(MEMORY)")
	assert.Equal(t, 1, strings.Count(dsn, "_pragma=cache_size("))
	assert.Contains(t, dsn, "_pragma=cache_size(-20000)")
}

func TestBuildDSN_PragmaValueEscaped(t *testing.T) {
	cfg := ConnectConfig{}
	WithPragma("x", "a+b&c%d")(&cfg)
	dsn := buildDSN("./app.db", cfg)

	q, err := url.ParseQuery(dsn[strings.Index(dsn, "?")+1:])
	require.NoError(t, err)
	assert.Contains(t, q["_pragma"], "x(a+b&c%d)")
}

func TestConnect_PragmaDefaultsAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	db, err := Connect(path, WithPragma("cache_size", "-20000"), WithPragma("synchronous", "NORMAL"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var n int
	require.NoError(t, db.Get(&n, "PRAGMA temp_store"))
	assert.Equal(t, 2, n)
	require.NoError(t, db.Get(&n, "PRAGMA journal_size_limit"))
	assert.Equal(t, 67108864, n)
	require.NoError(t, db.Get(&n, "PRAGMA cache_size"))
	assert.Equal(t, -20000, n)
	require.NoError(t, db.Get(&n, "PRAGMA synchronous"))
	assert.Equal(t, 1, n)
}

func TestConnect_TxLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tx.db")

	open := func(opts ...ConnectOption) *sqlx.DB {
		db, err := Connect(path, append(opts, WithBusyTimeout(50*time.Millisecond), WithMaxOpenConns(2))...)
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	t.Run("immediate", func(t *testing.T) {
		a, b := open(), open()
		txA, err := a.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = txA.Rollback() }()

		txB, err := b.BeginTxx(ctx, nil)
		if err == nil {
			_ = txB.Rollback()
		}
		require.Error(t, err)
		assert.Regexp(t, "(?i)busy|locked", err.Error())
	})

	t.Run("deferred", func(t *testing.T) {
		a, b := open(WithTxLock("deferred")), open(WithTxLock("deferred"))
		txA, err := a.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = txA.Rollback() }()

		txB, err := b.BeginTxx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, txB.Rollback())
	})
}

func TestConnect_ReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")
	db, err := Connect(path)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	ro, err := Connect(path, WithPragma("query_only", "1"))
	require.NoError(t, err)
	require.NoError(t, ro.Close())
}

func TestConnect_BusyTimeout(t *testing.T) {
	db, err := Connect(":memory:", WithBusyTimeout(2500*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var ms int
	require.NoError(t, db.Get(&ms, "PRAGMA busy_timeout"))
	assert.Equal(t, 2500, ms)
}

func TestConnectContext_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ConnectContext(ctx, filepath.Join(t.TempDir(), "test.db"))
	require.Error(t, err)
}

func TestMigrate_InvalidFS(t *testing.T) {
	var empty embed.FS
	err := Migrate(nil, MigrateConfig{FS: empty, Directory: "nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sqlite: creating migration source")
}

func TestMigrateDown_InvalidFS(t *testing.T) {
	var empty embed.FS
	err := MigrateDown(nil, MigrateConfig{FS: empty, Directory: "nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sqlite: creating migration source")
}

func TestMigrate_EmptyDirectory(t *testing.T) {
	var empty embed.FS
	err := Migrate(nil, MigrateConfig{FS: empty, Directory: ""})
	require.Error(t, err)
}

func TestMigrate_FullCycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.db")
	db, err := Connect(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
	}

	require.NoError(t, Migrate(db, cfg))

	version, dirty, err := MigrateVersion(db, cfg)
	require.NoError(t, err)
	assert.EqualValues(t, 1, version)
	assert.False(t, dirty)

	var count int
	require.NoError(t, db.Get(&count, "SELECT COUNT(*) FROM _hamr_migrate_test"))
	assert.Equal(t, 0, count)

	require.NoError(t, MigrateDown(db, cfg))

	_, _, err = MigrateVersion(db, cfg)
	require.NoError(t, err)
}

func TestMigrate_Steps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steps.db")
	db, err := Connect(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
	}

	require.NoError(t, MigrateSteps(db, cfg, 1))
	version, _, err := MigrateVersion(db, cfg)
	require.NoError(t, err)
	assert.EqualValues(t, 1, version)

	require.NoError(t, MigrateSteps(db, cfg, -1))
}

func TestMigrate_ForceClearsDirty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "force.db")
	db, err := Connect(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
	}

	require.NoError(t, MigrateForce(db, cfg, 0))
	version, dirty, err := MigrateVersion(db, cfg)
	require.NoError(t, err)
	assert.EqualValues(t, 0, version)
	assert.False(t, dirty)
}
