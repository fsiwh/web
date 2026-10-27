package database

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"log"
	"path/filepath"
	"testing"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/pressly/goose/v3"
)

func TestPublicIDMigrationPreservesKeysAndReservations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "publicids.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, source, goose.WithGoMigrations(utcEnforcementMigration()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO users(id,email,name,password) VALUES(987654321012345,'existing@example.org','Original','unchanged');
	INSERT INTO people(id,name,user_id,position) VALUES('legacy-person','Original',987654321012345,0);
	INSERT INTO events(id,title,cover_path) VALUES(987654321012346,'Original album','legacy/cover.jpg');`)
	if err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	if err := Migrate(path, logger); err != nil {
		t.Fatal(err)
	}
	userID, err := publicid.Lookup(ctx, db, "users", "987654321012345")
	if err != nil || !publicid.Valid(userID) {
		t.Fatal("missing backfill", userID, err)
	}
	if id, err := publicid.Resolve(ctx, db, "users", userID); err != nil || id != "987654321012345" {
		t.Fatal("changed existing key", id, err)
	}
	var name, password, cover string
	if err := db.QueryRow(`SELECT name,password FROM users WHERE id=987654321012345`).Scan(&name, &password); err != nil || name != "Original" || password != "unchanged" {
		t.Fatal("changed user data", err)
	}
	if err := db.QueryRow(`SELECT cover_path FROM events WHERE id=987654321012346`).Scan(&cover); err != nil || cover != "legacy/cover.jpg" {
		t.Fatal("changed stored key", err)
	}
	var memberID string
	if err := db.QueryRow(`SELECT public_id FROM public_ids WHERE namespace='members' AND record_id='legacy-person' AND retired=1`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(path, logger); err != nil {
		t.Fatal(err)
	}
	var again string
	if err := db.QueryRow(`SELECT public_id FROM public_ids WHERE namespace='members' AND record_id='legacy-person' AND retired=1`).Scan(&again); err != nil || again != memberID {
		t.Fatal("restart changed retired public ID", again, err)
	}
	var retired int
	if err := db.QueryRow(`SELECT retired FROM public_ids WHERE namespace='members' AND record_id='legacy-person'`).Scan(&retired); err != nil || retired != 1 {
		t.Fatal("reservation lost", err)
	}
	for _, statement := range []string{
		`DELETE FROM public_ids`,
		`UPDATE public_ids SET retired=0 WHERE namespace='members'`,
		`UPDATE public_ids SET public_id='AAAAAAAAAAA' WHERE namespace='members'`,
		`INSERT INTO public_ids(namespace,record_id,public_id) VALUES('members','another','AAAAAAAAAAB')`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("allowed reservation violation: %s", statement)
		}
	}
	// INSERT OR REPLACE must not bypass immutability through SQLite's implicit
	// deletes (which do not invoke delete triggers unless recursive_triggers=ON).
	for _, statement := range []string{
		`INSERT OR REPLACE INTO public_ids(namespace,record_id,public_id) VALUES('members','another','` + memberID + `')`,
		`INSERT OR REPLACE INTO public_ids(namespace,record_id,public_id) VALUES('members','legacy-person','AAAAAAAAAAA')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var reserved string
	if err := db.QueryRow(`SELECT public_id FROM public_ids WHERE namespace='members' AND record_id='legacy-person' AND retired=1`).Scan(&reserved); err != nil || reserved != memberID {
		t.Fatal("replace released a reservation", reserved, err)
	}
}

func TestOrganizationalDocumentsShareOnePublicURLNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "documents.db")
	if err := Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	db, err := NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const value = "m7Qp2Vx9aK4"
	if _, err := db.Exec(`INSERT INTO public_ids(namespace,record_id,public_id) VALUES('historical_documents','original.pdf',?)`, value); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"INSERT", "INSERT OR REPLACE"} {
		result, err := db.Exec(prefix+` INTO public_ids(namespace,record_id,public_id) VALUES('publications','new-publication',?)`, value)
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := result.RowsAffected(); n != 0 {
			t.Fatal("two documents acquired the same /documents/{id} address")
		}
	}
	if got, err := publicid.Resolve(context.Background(), db, "historical_documents", value); err != nil || got != "original.pdf" {
		t.Fatal("existing document reservation changed", got, err)
	}
}
