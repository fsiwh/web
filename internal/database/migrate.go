package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Migrate(dsn string, logger *log.Logger) error {
	logger.Println("Running database migrations...")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("goose: failed to open DB: %w", err)
	}
	defer db.Close()
	// Table rebuilds must preserve references to the original table names. This
	// connection is only used for migrations; runtime connections enforce FKs.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}

	if err := db.Ping(); err != nil {
		return fmt.Errorf("goose: failed to ping DB: %w", err)
	}

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

	logger.Println("Migrations applied successfully.")
	return nil
}
