package database

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestGalleryDefaultMigrationPreservesSelections(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "gallery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.UpTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password) VALUES(1,'gallery@portal.example.invalid','Gallery','unused');
INSERT INTO events(id,title) VALUES(10,'Gallery');
INSERT INTO media(id,event_id,user_id,access_key,mime_type,nbytes,gallery_visible) VALUES
(101,10,1,'media/first/source','image/jpeg',5,1),(102,10,1,'media/second/source','image/jpeg',5,0)`); err != nil {
		t.Fatal(err)
	}
	column := "gallery_visible"
	verify := func() {
		t.Helper()
		for _, row := range []struct {
			id, visible int
			key         string
		}{{101, 1, "media/first/source"}, {102, 0, "media/second/source"}} {
			var visible int
			var key string
			if err := db.QueryRow(`SELECT `+column+`,access_key FROM media WHERE id=?`, row.id).Scan(&visible, &key); err != nil || visible != row.visible || key != row.key {
				t.Fatalf("selection or file changed: %d %q %v", visible, key, err)
			}
		}
	}
	if _, err := p.UpTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	verify()
	if _, err := db.Exec(`INSERT INTO media(id,event_id,user_id,access_key,mime_type,nbytes) VALUES(103,10,1,'new','image/jpeg',5)`); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := db.QueryRow(`SELECT gallery_visible FROM media WHERE id=103`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("new media not opt-in: %d %v", visible, err)
	}
	if _, err := p.DownTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	verify()
	if _, err := p.UpTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	verify()
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	column = "homepage_visible"
	verify()
	if _, err := db.Exec(`INSERT INTO media(id,event_id,user_id,access_key,mime_type,nbytes) VALUES(104,10,1,'new-homepage','image/jpeg',5)`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT homepage_visible FROM media WHERE id=104`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("homepage default: %d %v", visible, err)
	}
}
