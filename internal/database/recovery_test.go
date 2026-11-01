package database

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestFullDatabaseRollsBackAndConnectionRecovers(t *testing.T) {
	db, err := NewConnection(filepath.Join(t.TempDir(), "site.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records(id INTEGER PRIMARY KEY, body BLOB);
		INSERT INTO records VALUES(1,x'01'); PRAGMA max_page_count=8`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO records VALUES(2,x'02')"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("INSERT INTO records VALUES(3,zeroblob(1048576))")
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_FULL {
		t.Fatalf("did not exercise database-full failure: %v", err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("failed transaction reported a successful commit")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil || count != 1 {
		t.Fatal("partial transaction survived full database", count, err)
	}
	if _, err := db.Exec("INSERT INTO records VALUES(4,x'04')"); err != nil {
		t.Fatal("connection unusable after failed commit", err)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("database damaged after full failure", integrity, err)
	}
}

func TestWALSnapshotRestoresCommittedStateAndConstraints(t *testing.T) {
	dir := t.TempDir()
	db, err := NewConnection(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA wal_autocheckpoint=0;
		CREATE TABLE accounts(id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE);
		CREATE TABLE sessions(id TEXT PRIMARY KEY, account INTEGER REFERENCES accounts(id) ON DELETE CASCADE);
		INSERT INTO accounts VALUES(1,'snapshot@example.org'); INSERT INTO sessions VALUES('synthetic-session',1)`); err != nil {
		t.Fatal(err)
	}
	// Keep the live handle open: committed rows still depend on the WAL. Use
	// SQLite's consistent snapshot facility, never a copy of just the main file.
	backup := filepath.Join(dir, "snapshot.db")
	if _, err := db.Exec("VACUUM INTO ?", backup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM accounts WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	restored, err := NewConnection(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var count int
	if err := restored.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 1 {
		t.Fatal("snapshot lost committed WAL data", count, err)
	}
	var integrity string
	if err := restored.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("restored snapshot corrupt", integrity, err)
	}
	if err := restored.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&count); err != nil || count != 0 {
		t.Fatal("restored snapshot has broken references", count, err)
	}
	if _, err := restored.Exec("INSERT INTO sessions VALUES('orphan',999)"); err == nil {
		t.Fatal("restored snapshot lost FK protection")
	}
	if _, err := restored.Exec("DELETE FROM accounts WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 0 {
		t.Fatal("restored cascade failed", count, err)
	}
}

func TestDatabaseOpenRejectsCorruptAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	// A nonexistent read-only path must not silently create an empty database.
	if db, err := NewConnection("file:" + filepath.Join(dir, "missing.db") + "?mode=ro"); err == nil {
		db.Close()
		t.Fatal("missing read-only database accepted")
	}
	path := filepath.Join(dir, "corrupt.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// A valid database header is needed for SQLite to materialize a file; the
	// writable_schema cookie change then makes the schema deliberately invalid.
	if _, err := db.Exec("CREATE TABLE original(id INTEGER); PRAGMA writable_schema=ON; UPDATE sqlite_schema SET sql='not valid SQL' WHERE name='original'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if db, err := NewConnection(path); err == nil {
		db.Close()
		t.Fatal("malformed database accepted")
	}
}
