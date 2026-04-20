package sqlite

import (
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
)

// MigrateConfig configures the migration runner.
type MigrateConfig struct {
	FS        embed.FS
	Directory string
}

// Migrate runs all up migrations from the embedded filesystem.
// migrate.ErrNoChange is silently ignored.
func Migrate(db *sqlx.DB, cfg MigrateConfig) error {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return err
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("sqlite: migrate up: %w", err)
	}
	return nil
}

// MigrateDown rolls back all migrations.
func MigrateDown(db *sqlx.DB, cfg MigrateConfig) error {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return err
	}

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("sqlite: migrate down: %w", err)
	}
	return nil
}

// MigrateSteps runs n migration steps. Positive n migrates up, negative migrates down.
func MigrateSteps(db *sqlx.DB, cfg MigrateConfig, n int) error {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return err
	}

	if err := m.Steps(n); err != nil {
		return fmt.Errorf("sqlite: migrate steps(%d): %w", n, err)
	}
	return nil
}

// MigrateVersion returns the current migration version and dirty flag.
func MigrateVersion(db *sqlx.DB, cfg MigrateConfig) (version uint, dirty bool, err error) {
	m, err := newMigrate(db, cfg)
	if err != nil {
		return 0, false, err
	}

	version, dirty, err = m.Version()
	if err != nil {
		if err == migrate.ErrNilVersion {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("sqlite: migrate version: %w", err)
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

	if err := m.Force(version); err != nil {
		return fmt.Errorf("sqlite: migrate force(%d): %w", version, err)
	}
	return nil
}

func newMigrate(db *sqlx.DB, cfg MigrateConfig) (*migrate.Migrate, error) {
	source, err := iofs.New(cfg.FS, cfg.Directory)
	if err != nil {
		return nil, fmt.Errorf("sqlite: creating migration source: %w", err)
	}

	dbDriver, err := migratesqlite.WithInstance(db.DB, &migratesqlite.Config{})
	if err != nil {
		return nil, fmt.Errorf("sqlite: creating migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "sqlite", dbDriver)
	if err != nil {
		return nil, fmt.Errorf("sqlite: creating migrator: %w", err)
	}
	return m, nil
}
