package db

import (
	"embed"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

func TestMigrateInvalidFS(t *testing.T) {
	var empty embed.FS
	err := Migrate(nil, MigrateConfig{
		FS:        empty,
		Directory: "nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: creating migration source")
}

func TestMigrateDownInvalidFS(t *testing.T) {
	var empty embed.FS
	err := MigrateDown(nil, MigrateConfig{
		FS:        empty,
		Directory: "nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: creating migration source")
}

func TestMigrateEmptyDirectory(t *testing.T) {
	var empty embed.FS
	err := Migrate(nil, MigrateConfig{
		FS:        empty,
		Directory: "",
	})
	require.Error(t, err)
}

func TestMigrateIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
	}

	// Run up.
	require.NoError(t, Migrate(db, cfg))

	// Idempotent — running again should not error.
	require.NoError(t, Migrate(db, cfg), "second Migrate should be idempotent")

	// Run down.
	require.NoError(t, MigrateDown(db, cfg))
}

func TestMigrateIntegrationVersionNoMigrations(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
	}

	// Clean slate.
	_ = MigrateDown(db, cfg)

	version, dirty, err := MigrateVersion(db, cfg)
	require.NoError(t, err)
	assert.Equal(t, uint(0), version)
	assert.False(t, dirty)
}

func TestMigrateStepsInvalidFS(t *testing.T) {
	var empty embed.FS
	err := MigrateSteps(nil, MigrateConfig{
		FS:        empty,
		Directory: "nonexistent",
	}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: creating migration source")
}

func TestMigrateVersionInvalidFS(t *testing.T) {
	var empty embed.FS
	_, _, err := MigrateVersion(nil, MigrateConfig{
		FS:        empty,
		Directory: "nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: creating migration source")
}

func TestMigrateForceInvalidFS(t *testing.T) {
	var empty embed.FS
	err := MigrateForce(nil, MigrateConfig{
		FS:        empty,
		Directory: "nonexistent",
	}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: creating migration source")
}

func TestMigrateIntegrationStepsVersionForce(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
	}

	// Clean slate.
	_ = MigrateDown(db, cfg)

	// Step up.
	require.NoError(t, MigrateSteps(db, cfg, 1))

	// Check version.
	version, dirty, err := MigrateVersion(db, cfg)
	require.NoError(t, err)
	assert.Equal(t, uint(1), version)
	assert.False(t, dirty)

	// Force version.
	require.NoError(t, MigrateForce(db, cfg, 1))
	version, dirty, err = MigrateVersion(db, cfg)
	require.NoError(t, err)
	assert.Equal(t, uint(1), version)
	assert.False(t, dirty)

	// Clean up.
	_ = MigrateDown(db, cfg)
}

func TestMigrateIntegrationCustomDriver(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
		Driver:    "postgres",
	}

	require.NoError(t, Migrate(db, cfg))
	require.NoError(t, MigrateDown(db, cfg))
}
