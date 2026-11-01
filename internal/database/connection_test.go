package database

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestConnectionReplacementPreservesIntegrity(t *testing.T) {
	// Caller-supplied DSN defaults must not weaken application invariants.
	db, err := NewConnection(filepath.Join(t.TempDir(), "site.db") + "?_fk=off&_timeout=0&_sync=OFF")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE parent(id INTEGER PRIMARY KEY);
		CREATE TABLE child(id INTEGER PRIMARY KEY,parent_id INTEGER REFERENCES parent(id) ON DELETE CASCADE);
		INSERT INTO parent VALUES(1); INSERT INTO child VALUES(1,1);`); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		for pragma, want := range map[string]any{"foreign_keys": int64(1), "busy_timeout": int64(5000), "synchronous": int64(2), "journal_mode": "wal"} {
			var got any
			if err := db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
				t.Fatalf("connection %d: %s=%v, want %v: %v", i, pragma, got, want, err)
			}
		}
		if _, err := db.Exec("INSERT INTO child VALUES(2,999)"); err == nil {
			t.Fatal("orphan accepted after connection replacement")
		}
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		err = conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
		if !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("DELETE FROM parent WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRow("SELECT count(*) FROM child").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("cascade was lost", remaining, err)
	}
}

func TestConnectionRejectsUnavailableWAL(t *testing.T) {
	if db, err := NewConnection(":memory:"); err == nil {
		db.Close()
		t.Fatal("silently accepted memory journal instead of WAL")
	}
}

func TestConnectionCancellationReleasesPoolAndWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.db")
	db, err := NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := db.Exec("CREATE TABLE changes(id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO changes VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	// Both a pool waiter and an independent SQLite writer must obey deadlines.
	waitCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err = db.ExecContext(waitCtx, "SELECT 1")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pool wait ignored deadline: %v", err)
	}
	waitCtx, stop = context.WithTimeout(context.Background(), 50*time.Millisecond)
	start := time.Now()
	_, err = other.ExecContext(waitCtx, "INSERT INTO changes VALUES(2)")
	stop()
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("writer wait ignored cancellation: %v", err)
	}
	cancel()
	// Request rollback; the next pool acquisition must also tolerate
	// database/sql's asynchronous cancellation rollback still being in progress.
	_ = tx.Rollback()
	recovery, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	if _, err := db.ExecContext(recovery, "INSERT INTO changes VALUES(3)"); err != nil {
		t.Fatal("pool not usable after rollback", err)
	}
	if _, err := other.ExecContext(recovery, "INSERT INTO changes VALUES(4)"); err != nil {
		t.Fatal("writer lock leaked", err)
	}
	var count int
	if err := db.QueryRowContext(recovery, "SELECT count(*) FROM changes").Scan(&count); err != nil || count != 2 {
		t.Fatal("cancelled transaction partially committed", count, err)
	}
}

func TestStaleSnapshotCannotOverwriteConcurrentWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.db")
	db, err := NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := db.Exec("CREATE TABLE counter(value INTEGER); INSERT INTO counter VALUES(0)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value int
	if err := tx.QueryRow("SELECT value FROM counter").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec("UPDATE counter SET value=1"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("UPDATE counter SET value=?", value+2)
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 517 { // SQLITE_BUSY_SNAPSHOT
		t.Fatalf("stale transaction did not fail as a stale snapshot: %v", err)
	}
	tx.Rollback()
	if err := db.QueryRow("SELECT value FROM counter").Scan(&value); err != nil || value != 1 {
		t.Fatal("concurrent update was lost", value, err)
	}
}
