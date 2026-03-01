package db

import (
	"embed"
	"os"
	"testing"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

func TestMigrateInvalidFS(t *testing.T) {
	var empty embed.FS
	// sqlx.DB is nil but we should fail before needing it.
	err := Migrate(nil, MigrateConfig{
		FS:        empty,
		Directory: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for invalid FS/directory")
	}
}

func TestMigrateIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := MigrateConfig{
		FS:        testMigrations,
		Directory: "testdata/migrations",
	}

	// Run up.
	if err := Migrate(db, cfg); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Idempotent — running again should not error.
	if err := Migrate(db, cfg); err != nil {
		t.Fatalf("Migrate (idempotent): %v", err)
	}

	// Run down.
	if err := MigrateDown(db, cfg); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
}
