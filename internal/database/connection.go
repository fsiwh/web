package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"modernc.org/sqlite"
)

func NewConnection(dsn string) (*sql.DB, error) {
	return openConnection(dsn, false)
}

func openConnection(dsn string, migration bool) (*sql.DB, error) {
	connector, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	db := sql.OpenDB(configuredConnector{Connector: connector, migration: migration})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// Configure every physical connection, including replacements after cancellation
// or driver errors. A pool size of one does not make connection state permanent.
type configuredConnector struct {
	driver.Connector
	migration bool
}

func (c configuredConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	configure := func() error {
		exec := conn.(driver.ExecerContext)
		statements := []string{
			"PRAGMA busy_timeout=5000",
			"PRAGMA journal_mode=WAL",
			// Account changes and revocations must survive a hard reset, not just
			// a process crash. NORMAL does not sync the WAL at each commit.
			"PRAGMA synchronous=FULL",
			"PRAGMA foreign_keys=ON",
		}
		if c.migration {
			// Table rebuilds need enforcement disabled on every migration
			// connection too. The final foreign_key_check validates their result.
			statements = []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=OFF"}
		}
		for _, statement := range statements {
			if _, err := exec.ExecContext(ctx, statement, nil); err != nil {
				return fmt.Errorf("configure SQLite: %w", err)
			}
		}
		if c.migration {
			return nil
		}
		rows, err := conn.(driver.QueryerContext).QueryContext(ctx, "PRAGMA journal_mode", nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		values := make([]driver.Value, 1)
		if err := rows.Next(values); err != nil {
			return err
		}
		if values[0] != "wal" {
			return fmt.Errorf("SQLite requires WAL mode, got %v", values[0])
		}
		return nil
	}
	if err := configure(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
