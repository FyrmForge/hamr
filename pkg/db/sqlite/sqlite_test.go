package sqlite

import (
	"context"
	"embed"
	"path/filepath"
	"testing"
	"time"

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
	assert.Equal(t, "./app.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)", dsn)
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
