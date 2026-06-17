package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
)

// MigrateConfig configures the migration runner.
type MigrateConfig struct {
	FS        embed.FS
	Directory string
	Driver    string
}

// Migrate runs all up migrations from the embedded filesystem.
// ErrNoChange is silently ignored.
func Migrate(db *sqlx.DB, cfg MigrateConfig) error {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// MigrateDown rolls back all migrations.
func MigrateDown(db *sqlx.DB, cfg MigrateConfig) error {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("db: migrate down: %w", err)
	}
	return nil
}

// MigrateSteps runs n migration steps. Positive n migrates up, negative migrates down.
func MigrateSteps(db *sqlx.DB, cfg MigrateConfig, n int) error {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Steps(n); err != nil {
		return fmt.Errorf("db: migrate steps(%d): %w", n, err)
	}
	return nil
}

// MigrateVersion returns the current migration version and dirty flag.
func MigrateVersion(db *sqlx.DB, cfg MigrateConfig) (version uint, dirty bool, err error) {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrate(m)

	version, dirty, err = m.Version()
	if err != nil {
		if err == migrate.ErrNilVersion {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("db: migrate version: %w", err)
	}
	return version, dirty, nil
}

// MigrateForce sets the migration version without running any migrations.
// Use this to fix a dirty migration state.
func MigrateForce(db *sqlx.DB, cfg MigrateConfig, version int) error {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Force(version); err != nil {
		return fmt.Errorf("db: migrate force(%d): %w", version, err)
	}
	return nil
}

// closeMigrate releases the migrator's resources: the iofs source and the
// single *sql.Conn the postgres driver borrowed from the pool (see newMigrate).
// Without this, every Migrate* call permanently pins one pooled connection.
//
// This is only safe because newMigrate builds the driver with WithConnection,
// NOT WithInstance. WithInstance stores the caller's *sql.DB on the driver and
// its Close() then closes the shared pool — calling it here would shut the
// app's database after the first migration. WithConnection leaves that field
// nil, so Close() merely returns the borrowed *sql.Conn to the pool.
func closeMigrate(m *migrate.Migrate) {
	if m == nil {
		return
	}
	_, _ = m.Close()
}

func newMigrate(db *sqlx.DB, cfg MigrateConfig) (*migrate.Migrate, error) {
	driver := cfg.Driver
	if driver == "" {
		driver = "postgres"
	}

	source, err := iofs.New(cfg.FS, cfg.Directory)
	if err != nil {
		return nil, fmt.Errorf("db: creating migration source: %w", err)
	}

	// Borrow one connection from the pool for the migrator. We must use
	// WithConnection rather than WithInstance: WithInstance stores db.DB on the
	// driver and its Close() closes the shared pool (golang-migrate v4.19.1,
	// postgres.go: `px.db = instance`). WithConnection leaves that nil, so
	// closeMigrate's m.Close() only returns this conn to the pool.
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("db: checking out migration conn: %w", err)
	}

	dbDriver, err := postgres.WithConnection(context.Background(), conn, &postgres.Config{})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("db: creating migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, driver, dbDriver)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("db: creating migrator: %w", err)
	}
	return m, nil
}
