package database

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"log"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestAccountPolicyMigrationPreservesUsersAndReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(context.Background(), 12); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified,password_reset_token,password_reset_expires) VALUES(42,'member@fsv-wh.de','Existing','hash','admin',1,1,'token','2030-01-01T00:00:00Z');
INSERT INTO people(id,name,user_id,position)VALUES('person','Existing',42,0);
INSERT INTO web_sessions(id,user_id,expires_at) VALUES('session',42,'2030-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	read := func() []any {
		t.Helper()
		rows, err := db.Query("SELECT id,email,name,password,role,active,verified,verified_at,verification_token,program_id,theme,private,avatar_url,created_at,deleted_at,password_reset_token,password_reset_expires FROM users WHERE id=42")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if !rows.Next() {
			t.Fatal("user missing")
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		return values
	}
	before := read()
	if err := Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, read()) {
		t.Fatal("users-table rebuild changed existing account data")
	}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; INSERT INTO users(email,name,password)VALUES('person@new.example.org','New','hash')`); err != nil {
		t.Fatal("old domain constraint remains", err)
	}
	if _, err = db.Exec("INSERT INTO user_functions(user_id,function_id) VALUES(999,'missing')"); err == nil {
		t.Fatal("foreign-key enforcement was lost")
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM web_sessions WHERE user_id=42").Scan(&count); err != nil || count != 1 {
		t.Fatal("session changed during rebuild", err)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration introduced broken references")
	}
	if err = db.QueryRow("SELECT count(*) FROM organization").Scan(&count); err != nil || count != 0 {
		t.Fatal("schema migration inserted organization content", err)
	}
}

func TestEmbeddedMigrationsPreserveDataOnRestart(t *testing.T) {
	// The executable must migrate without SQL files in its working directory.
	t.Chdir(t.TempDir())
	dsn := filepath.Join(t.TempDir(), "sqlite.db")
	logger := log.New(io.Discard, "", 0)
	if err := Migrate(dsn, logger); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users (id, email, name, password, password_reset_token) VALUES (42, 'kim@studmail.w-hs.de', 'Kim', 'existing-hash', 'existing-token')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(dsn, logger); err != nil {
		t.Fatal(err)
	}
	var password, token string
	if err := db.QueryRow(`SELECT password, password_reset_token FROM users WHERE id = 42`).Scan(&password, &token); err != nil {
		t.Fatal(err)
	}
	if password != "existing-hash" || token != "existing-token" {
		t.Fatal("reapplying migrations changed an existing account")
	}
}

func TestArchiveMigrationPreservesAliasesHistoriesAndQualifications(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO programs(id,name) VALUES(1,'Informatik (B. Sc.)'),(2,'Wirtschaftsinformatik (B. Sc.)'),(3,'Informatik (M. Sc.)');
    INSERT INTO program_versions(program_id,name) VALUES(1,'PO2016'),(2,'PO2023'),(3,'PO2016');
    INSERT INTO modules(id,program_id,name) VALUES(10,1,'Mathematik'),(20,2,'Mathematik'),(30,3,'Mathematik');
    INSERT INTO users(id,email,name,password) VALUES(1,'migration@fsv-wh.de','Name','unchanged');
    INSERT INTO archive(id,group_id,user_id,module_id,version,exam_date,edit_version,is_latest,access_key,mime_type,nbytes,checksum) VALUES
    (100,100,1,10,'PO2016','2026-01-01',1,0,'a','application/pdf',5,'old'),
    (101,100,1,10,'PO2016','2026-01-01',2,1,'b','application/pdf',6,'same'),
    (200,200,1,20,'PO2023','2026-01-01',1,0,'c','application/pdf',5,'old'),
    (201,200,1,20,'PO2023','2026-01-01',2,1,'d','application/pdf',6,'same'),
    (300,300,1,30,'PO2016','2026-01-01',1,1,'e','application/pdf',6,'same');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	for query, want := range map[string]int{
		"SELECT COUNT(*) FROM archive":                                                        5,
		"SELECT COUNT(*) FROM archive_objects":                                                2,
		"SELECT COUNT(*) FROM archive_entry_aliases WHERE legacy_id!=canonical_id":            2,
		"SELECT COUNT(*) FROM archive_assignments WHERE archive_id=101":                       2,
		"SELECT COUNT(*) FROM study_programs":                                                 2,
		"SELECT COUNT(*) FROM program_qualifications WHERE degree='Bachelor'":                 2,
		"SELECT COUNT(*) FROM archive_entry_aliases WHERE legacy_id=300 AND canonical_id=300": 1,
		"SELECT COUNT(*) FROM archive WHERE is_latest=1":                                      3,
	} {
		if err := db.QueryRow(query).Scan(&count); err != nil || count != want {
			t.Fatalf("%s: %d want %d: %v", query, count, want, err)
		}
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarMigrationPreservesLegacyDiscussions(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "calendar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO users(id,email,name,password) VALUES(1,'calendar@fsv-wh.de','Name','unchanged');
	INSERT INTO discussion_posts(id,title,body,user_id,type,event_date,location,deleted_at) VALUES
	(10,'Grillabend','Original body',1,'event','2026-06-09T17:00','Innenhof',NULL),
	(20,'Diskussion','Original discussion',1,'discussion',NULL,NULL,NULL),
	(30,'Gelöscht','Deleted',1,'event',NULL,NULL,'deleted');
	INSERT INTO discussion_comments(id,post_id,user_id,text) VALUES(100,10,1,'Original comment');
	INSERT INTO discussion_votes(post_id,user_id,vote) VALUES(10,1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	var originalUpdated string
	if err := db.QueryRow(`SELECT updated_at FROM discussion_posts WHERE id=10`).Scan(&originalUpdated); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var updated, eventDate string
	if err := db.QueryRow(`SELECT updated_at,event_date FROM discussion_posts WHERE id=10`).Scan(&updated, &eventDate); err != nil || updated != originalUpdated || eventDate != "2026-06-09T17:00" {
		t.Fatal("calendar migration changed publication metadata or event time", updated, eventDate, err)
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM calendar_events`:                          2,
		`SELECT COUNT(*) FROM calendar_events WHERE deleted_at IS NULL`: 1,
		`SELECT COUNT(*) FROM calendar_events WHERE id=10 AND legacy_post_id=10 AND body='Original body' AND event_date='2026-06-09T17:00' AND location='Innenhof'`: 1,
		`SELECT COUNT(*) FROM discussion_posts`:                                                 3,
		`SELECT COUNT(*) FROM discussion_comments WHERE post_id=10 AND text='Original comment'`: 1,
		`SELECT COUNT(*) FROM discussion_votes WHERE post_id=10 AND vote=1`:                     1,
	} {
		var count int
		if err := db.QueryRow(query).Scan(&count); err != nil || count != want {
			t.Fatalf("%s: %d %v", query, count, err)
		}
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}
