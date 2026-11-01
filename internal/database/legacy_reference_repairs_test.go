package database

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"log"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestLegacyReferenceRepairPreservesAccountsAndOrphanNotifications(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, source, goose.WithGoMigrations(utcEnforcementMigration(), publicIDsMigration(), publicIDSequencesMigration(), programNamesMigration(), programVersionsMigration(), unifiedUsersMigration()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 32); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO programs(id,name) VALUES(1,'Existing');
		INSERT INTO users(id,email,name,password,role,active,verified,program_id) VALUES
		(42,'legacy@example.org','Legacy account','preserve-hash','admin',1,1,999),
		(43,'valid@example.org','Valid account','valid-hash','user',1,1,1);
		INSERT INTO notifications(id,user_id,title,message,type,link,read,created_at,updated_at,deleted_at) VALUES
		(1,999,'Original title','Original message','archive','/original',1,'2026-01-01T00:00:00Z','2026-01-02T00:00:00Z',NULL),
		(2,42,'Valid title','Valid message','archive','/valid',0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',NULL);`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	var name, email, password, role string
	var active, verified int
	var program sql.NullInt64
	if err := db.QueryRow("SELECT name,email,password,role,active,verified,program_id FROM users WHERE id=42").Scan(&name, &email, &password, &role, &active, &verified, &program); err != nil {
		t.Fatal(err)
	}
	if name != "Legacy account" || email != "legacy@example.org" || password != "preserve-hash" || role != "admin" || active != 1 || verified != 1 || program.Valid {
		t.Fatal("repair changed account identity or permissions")
	}
	if err := db.QueryRow("SELECT program_id FROM users WHERE id=43").Scan(&program); err != nil || !program.Valid || program.Int64 != 1 {
		t.Fatal("repair changed valid association", program, err)
	}
	var oldProgram, orphanUser, unread int
	var title, message, link, created, updated string
	var deleted sql.NullString
	if err := db.QueryRow("SELECT json_extract(original,'$.program_id') FROM legacy_reference_repairs WHERE source_table='users' AND record_id=42").Scan(&oldProgram); err != nil || oldProgram != 999 {
		t.Fatal("invalid program reference not preserved", oldProgram, err)
	}
	if err := db.QueryRow(`SELECT json_extract(original,'$.user_id'),json_extract(original,'$.title'),json_extract(original,'$.message'),json_extract(original,'$.link'),json_extract(original,'$.read'),json_extract(original,'$.created_at'),json_extract(original,'$.updated_at'),json_extract(original,'$.deleted_at') FROM legacy_reference_repairs WHERE source_table='notifications' AND record_id=1`).Scan(&orphanUser, &title, &message, &link, &unread, &created, &updated, &deleted); err != nil {
		t.Fatal(err)
	}
	if orphanUser != 999 || title != "Original title" || message != "Original message" || link != "/original" || unread != 1 || created != "2026-01-01T00:00:00Z" || updated != "2026-01-02T00:00:00Z" || deleted.Valid {
		t.Fatal("orphan notification not preserved intact")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM notifications WHERE id=2 AND user_id=42").Scan(&count); err != nil || count != 1 {
		t.Fatal("valid notification lost", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&count); err != nil || count != 0 {
		t.Fatal("repair left invalid references", count, err)
	}
	if err := Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal("restart after repair failed", err)
	}
	if err := db.QueryRow("SELECT count(*) FROM legacy_reference_repairs").Scan(&count); err != nil || count != 2 {
		t.Fatal("restart duplicated recovery records", count, err)
	}
}
