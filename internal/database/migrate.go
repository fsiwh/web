package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Migrate(dsn string, logger *log.Logger) error {
	logger.Println("Running database migrations...")
	db, err := openConnection(dsn, true)
	if err != nil {
		return fmt.Errorf("goose: failed to open DB: %w", err)
	}
	defer db.Close()

	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("goose: embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, files, goose.WithGoMigrations(utcEnforcementMigration(), publicIDsMigration(), publicIDSequencesMigration(), programNamesMigration(), programVersionsMigration(), unifiedUsersMigration()))
	if err != nil {
		return fmt.Errorf("goose: create migration provider: %w", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("goose: up failed: %w", err)
	}
	// Re-enabling enforcement does not validate rows written while it was off.
	// Do not start the application with references broken by an upgrade or an
	// earlier connection whose foreign-key enforcement was disabled.
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("goose: check foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		// Avoid logging row contents or account identifiers in startup errors.
		return fmt.Errorf("goose: foreign-key violations remain after migration")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("goose: check foreign keys: %w", err)
	}

	logger.Println("Migrations applied successfully.")
	return nil
}
