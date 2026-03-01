package db

import (
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

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("db: migrate down: %w", err)
	}
	return nil
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

	dbDriver, err := postgres.WithInstance(db.DB, &postgres.Config{})
	if err != nil {
		return nil, fmt.Errorf("db: creating migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, driver, dbDriver)
	if err != nil {
		return nil, fmt.Errorf("db: creating migrator: %w", err)
	}
	return m, nil
}
