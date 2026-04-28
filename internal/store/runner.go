package store

// runMigrations executes all pending golang-migrate migrations against the
// Postgres database. It embeds the SQL files from the top-level migrations/
// directory via the migrations.FS embed.FS (package
// github.com/TechXTT/xolto/migrations).
//
// Design choices:
//
//   - Driver: pgx/v5 via golang-migrate's pgx/v5 database driver.
//   - Source: iofs (embed.FS), so the single binary works regardless of
//     container filesystem layout.
//   - Error handling: migrate.ErrNoChange is NOT an error — it means the
//     database is already up-to-date. Any other error is fatal; the caller
//     (NewPostgresWithPool) returns it so cmd/server/main.go can os.Exit(1).
//
// First-deploy note:
//
//	If the production database was bootstrapped by the old inline-CREATE path
//	(i.e., schema_migrations table does not exist yet), golang-migrate will
//	attempt to apply ALL migrations from version 1. Because every statement
//	uses CREATE TABLE IF NOT EXISTS / ALTER ... ADD COLUMN IF NOT EXISTS, this
//	is safe and idempotent. The runner will create schema_migrations and mark
//	all 17 versions as applied.
//
//	If for any reason you need to tell golang-migrate "versions 1-16 are
//	already applied, only run 17", connect to the production database via
//	psql or the Railway console and run:
//	  CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty bool NOT NULL);
//	  INSERT INTO schema_migrations (version, dirty) VALUES (16, false) ON CONFLICT DO NOTHING;
//	Then restart the service. The runner will apply only version 17 onward.
//	See: https://github.com/golang-migrate/migrate/blob/master/FAQ.md#forcing-a-migration

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	// Alias to avoid collision with the package-level SQLite migrate() function
	// in store.go. The identifier "migrate" is already taken in this package.
	gmigrate "github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/TechXTT/xolto/migrations"
)

// runPostgresMigrations applies all pending SQL migrations from the embedded
// migrations.FS. Returns nil when the database is already at the latest version.
func runPostgresMigrations(db *sql.DB) error {
	// iofs source reads from the embedded FS.
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migration runner: creating iofs source: %w", err)
	}

	// pgx/v5 driver wraps the existing *sql.DB connection.
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("migration runner: creating pgx driver: %w", err)
	}

	m, err := gmigrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("migration runner: initialising migrator: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, gmigrate.ErrNoChange) {
		// Log the current dirty version to aid debugging.
		version, dirty, vErr := m.Version()
		if vErr == nil {
			slog.Error("postgres migration runner: Up() failed",
				"error", err,
				"version", version,
				"dirty", dirty,
			)
		} else {
			slog.Error("postgres migration runner: Up() failed", "error", err)
		}
		return fmt.Errorf("migration runner: applying migrations: %w", err)
	}

	version, _, vErr := m.Version()
	if vErr == nil {
		slog.Info("postgres migration runner: database is up to date", "version", version)
	}

	return nil
}
