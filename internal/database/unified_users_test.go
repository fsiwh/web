package database

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func legacyUsersDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, source, goose.WithGoMigrations(utcEnforcementMigration(), publicIDsMigration(), publicIDSequencesMigration(), programNamesMigration(), programVersionsMigration()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(context.Background(), 29); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestUnifiedUsersMigrationPreservesAccountsAndTeam(t *testing.T) {
	db, path := legacyUsersDatabase(t)
	if _, err := db.Exec(`
		INSERT INTO users(id,name,email,password,role,active,verified) VALUES(42,'Account Name','UPPER@FSV-WH.DE','existing-hash','admin',1,1);
		INSERT INTO people(id,name,email,user_id,position,public_name) VALUES('linked','Stale Name','public@example.org',42,0,'Public Display Name'),('unlinked','New Account','new@example.org',NULL,1,'');
		INSERT INTO responsibilities(id,group_name,title,email,position) VALUES('finance','board','Finanzen','finance@example.org',0),('sport','departments','Sport','',1);
		INSERT INTO responsibility_members(responsibility_id,person_id,position) VALUES('finance','linked',0),('sport','linked',1),('sport','unlinked',0);
		INSERT INTO organization(id,data) VALUES(1,'{"ChairResponsibility":"finance","LegalUpdated":"2000-01-01"}');
	`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	var name, email, password, role, publicEmail string
	if err := db.QueryRow(`SELECT name,email,password,role,public_email FROM users WHERE id=42`).Scan(&name, &email, &password, &role, &publicEmail); err != nil || name != "Account Name" || email != "upper@fsv-wh.de" || password != "existing-hash" || role != "admin" || publicEmail != "public@example.org" {
		t.Fatal("account data changed", name, email, password, role, publicEmail, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM user_functions WHERE user_id=42`).Scan(&count); err != nil || count != 2 {
		t.Fatal("assignments lost", count, err)
	}
	var pending, active int
	if err := db.QueryRow(`SELECT password,role,invitation_pending,active FROM users WHERE email='new@example.org'`).Scan(&password, &role, &pending, &active); err != nil || password != "!" || role != "editor" || pending != 1 || active != 1 {
		t.Fatal("unlinked profile not migrated into pending account", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('people','people_state','responsibilities','responsibility_members')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy identity tables remain", err)
	}
	var org int
	var publicName string
	if err := db.QueryRow(`SELECT public_name FROM users WHERE id=42`).Scan(&publicName); err != nil || publicName != "Public Display Name" {
		t.Fatal("public name lost", err)
	}
	if err := db.QueryRow(`SELECT organization_id FROM functions WHERE id='finance'`).Scan(&org); err != nil || org != 1 {
		t.Fatal("function organization missing", err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("broken foreign keys")
	}
	rows.Close()
	if err := Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal("migration not restartable", err)
	}
	for _, statement := range []string{
		`INSERT INTO users(email,name,password) VALUES('UPPER@fsv-wh.de','Duplicate','!')`,
		`INSERT INTO users(email,name,password) VALUES('upper@fsv-wh.de','Duplicate','!')`,
		`UPDATE users SET email='UPPER@FSV-WH.DE' WHERE id=42`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatal("email boundary accepted invalid value", statement)
		}
	}
}

func TestUnifiedUsersMigrationRejectsUnresolvableProfiles(t *testing.T) {
	for _, entry := range []struct{ name, seed, want string }{
		{"missing email", `INSERT INTO people(id,name,email,position) VALUES('missing','No Mail','',0)`, "needs a usable account email"},
		{"case collision", `INSERT INTO users(email,name,password) VALUES('same@example.org','One','!'),('SAME@example.org','Two','!')`, "case-insensitive account email collisions"},
		{"duplicate profiles", `INSERT INTO people(id,name,email,position) VALUES('one','One','same@example.org',0),('two','Two','same@example.org',1)`, "resolve to the same account"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			db, path := legacyUsersDatabase(t)
			if _, err := db.Exec(entry.seed); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(path, log.New(io.Discard, "", 0)); err == nil || !strings.Contains(err.Error(), entry.want) {
				t.Fatal("unexpected migration outcome", err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='people'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("failed migration lost legacy data", err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='user_functions'`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed migration partially applied", err)
			}
		})
	}
}
