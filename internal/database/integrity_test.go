package database

import (
	"bytes"
	"database/sql"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateRejectsBrokenReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.db")
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	if err := Migrate(path, logger); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Simulate a historical connection without enforcement, not an application
	// write. Re-enabling enforcement does not retroactively reject these rows.
	if _, err := db.Exec(`INSERT INTO web_sessions(id,user_id,expires_at) VALUES('private-session-value',999999,'2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	err = Migrate(path, logger)
	if err == nil || !strings.Contains(err.Error(), "foreign-key violations") {
		t.Fatalf("migration accepted broken references: %v", err)
	}
	if strings.Contains(logs.String(), "successfully") || strings.Contains(err.Error(), "private-session-value") {
		t.Fatal("misleading or sensitive diagnostic", logs.String(), err)
	}
	if _, err := db.Exec("DELETE FROM web_sessions"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(path, logger); err != nil {
		t.Fatal("migration could not resume after repair", err)
	}
}
