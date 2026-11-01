package events

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/storage"
	"github.com/fachschaftinformatik/web/storage/disk"
)

func eventService(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	path := testdb.Path(t)
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES
	(1,'admin@council.example.invalid','Admin','unused','admin',1,1),(2,'editor@council.example.invalid','Editor','unused','editor',1,1),
	(3,'user@council.example.invalid','User','unused','user',1,1),(4,'inactive@council.example.invalid','Inactive','unused','admin',0,1),
	(5,'unverified@council.example.invalid','Unverified','unused','editor',1,0),(6,'deleted@council.example.invalid','Deleted','unused','editor',1,1);
	UPDATE users SET deleted_at='2026-01-01T00:00:00Z' WHERE id=6;`)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	storage, err := disk.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	return NewService(db, storage), db, root
}
func coverPhoto(t *testing.T) []byte {
	t.Helper()
	return eventPhoto(t, 800, 1800)
}

func eventPhoto(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	i := image.NewNRGBA(image.Rect(0, 0, width, height))
	i.Set(0, 0, color.NRGBA{R: 255, A: 255})
	if err := png.Encode(&b, i); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDeletedHighestEventKeyIsNeverReused(t *testing.T) {
	s, db, _ := eventService(t)
	ctx := context.Background()
	d := Draft{Title: "An event", Body: "An event description."}
	first, err := s.Save(ctx, 1, "", d, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Lookup(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM calendar_events WHERE id=?`, old.InternalID); err != nil {
		t.Fatal(err)
	}
	next, err := s.Save(ctx, 1, "", d, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Lookup(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if first == next || old.InternalID == current.InternalID {
		t.Fatal("deleted event identity was reused")
	}
	if _, err := s.Save(ctx, 1, next, d, "", nil); err != nil {
		t.Fatal("existing event update:", err)
	}
}

func TestLinkedPostResolvesItsEventRatherThanEqualID(t *testing.T) {
	s, db, _ := eventService(t)
	if _, err := db.Exec(`INSERT INTO discussion_posts(id,title,body,type,user_id) VALUES(50,'Original post','Body','event',1);
		INSERT INTO calendar_events(id,title,body,user_id,legacy_post_id) VALUES
		(60,'Linked event','Body',1,50),(50,'Unrelated event','Body',1,NULL)`); err != nil {
		t.Fatal(err)
	}
	postRef := reference(t, db, "posts", "50")
	event, err := s.ForPost(context.Background(), postRef)
	if err != nil || event.ID != reference(t, db, "events", "60") {
		t.Fatalf("linked event = %q, %v", event.ID, err)
	}
	if _, err := db.Exec(`UPDATE calendar_events SET deleted_at='2026-10-18T00:00:00Z' WHERE id=60`); err != nil {
		t.Fatal(err)
	}
	for _, post := range []string{postRef, "999"} {
		if _, err := s.ForPost(context.Background(), post); !errors.Is(err, ErrNotFound) {
			t.Fatalf("post %s: %v", post, err)
		}
	}
}

func TestStandaloneEventsPersistCoversAndRestrictPublishing(t *testing.T) {
	s, db, _ := eventService(t)
	ctx := context.Background()
	d := Draft{Title: "Grillabend", Body: "Gemeinsam grillen im Innenhof.", Date: "30.01.2027", Time: "17:00", Location: "Innenhof"}
	for _, user := range []int64{0, 3, 4, 5, 6, 999} {
		if _, err := s.Save(ctx, user, "", d, "", nil); !errors.Is(err, ErrForbidden) {
			t.Fatalf("user %d: %v", user, err)
		}
	}
	value, err := s.Save(ctx, 1, "", d, `<p><strong>Gemeinsam grillen</strong></p><script>bad()</script>`, &CoverUpload{Data: coverPhoto(t), Alt: "Ein Grill im Innenhof"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Lookup(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if !publicid.Valid(value) {
		t.Fatalf("noncanonical public event ID %q", value)
	}
	if _, err := s.Lookup(ctx, e.InternalID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decimal event lookup alias: %v", err)
	}
	if _, err := s.Save(ctx, 1, e.InternalID, d, "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decimal event edit alias: %v", err)
	}
	if _, err := s.OpenCover(ctx, e.InternalID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decimal cover alias: %v", err)
	}
	if strings.Contains(e.Body, "bad()") || e.EventDate != "2027-01-30T16:00:00Z" || e.CoverWidth != 711 || e.CoverHeight != 1600 || e.LegacyPostID.Valid {
		t.Fatalf("event: %+v", e)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM discussion_posts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("created discussion posts: %d %v", count, err)
	}
	object, err := s.OpenCover(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	photo, err := jpeg.Decode(object)
	object.Close()
	if err != nil || photo.Bounds().Dy() != 1600 || object.Info.ContentType != "image/jpeg" {
		t.Fatal("stored JPEG preview missing")
	}
	oldKey := e.CoverKey
	d.Title = "Grillabend mit Spielen"
	if _, err := s.Save(ctx, 2, value, d, "", nil); err != nil {
		t.Fatal(err)
	}
	e, err = NewService(db, s.storage).Lookup(ctx, value)
	if err != nil || e.CoverKey != oldKey || e.Title != d.Title {
		t.Fatal("edit lost cover or title")
	}
	if _, err := s.Save(ctx, 3, value, d, "", nil); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary member edited event")
	}
	if _, err := s.Save(ctx, 2, value, d, "", &CoverUpload{Data: []byte("<svg></svg>"), Alt: "Bild"}); err == nil {
		t.Fatal("accepted active SVG")
	}
	e, _ = s.Lookup(ctx, value)
	if e.CoverKey != oldKey {
		t.Fatal("invalid upload changed registered image")
	}
	if _, err := s.Save(ctx, 2, value, d, "", &CoverUpload{Data: eventPhoto(t, 16, 24), Alt: "Neues Titelbild"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.storage.GetObject(ctx, oldKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("replaced image was not removed")
	}
	if _, err := s.Save(ctx, 2, value, d, "", &CoverUpload{Remove: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenCover(ctx, value); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed image still public")
	}
	if _, err := db.Exec(`UPDATE calendar_events SET deleted_at='2026-01-01T00:00:00Z' WHERE id=?`, e.InternalID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, value); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted event visible")
	}
	if _, err := s.Save(ctx, 1, value, d, "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatal("edited deleted event")
	}
}

func TestInvalidUploadsAndFailedTransactionsPreserveStorage(t *testing.T) {
	s, db, root := eventService(t)
	ctx := context.Background()
	d := Draft{Title: "Party", Body: "Ein Abend mit Musik und Spielen."}
	photo := eventPhoto(t, 16, 24)
	for _, cover := range []CoverUpload{{Data: []byte("not an image"), Alt: "Test"}, {Data: bytes.Repeat([]byte{'x'}, MaxCoverBytes+1), Alt: "Test"}, {Data: photo}, {Data: photo, Remove: true, Alt: "Test"}} {
		if _, err := s.Save(ctx, 1, "", d, "", &cover); err == nil {
			t.Fatal("invalid upload saved")
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_event BEFORE INSERT ON calendar_events BEGIN SELECT RAISE(ABORT,'test rollback'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, 1, "", d, "", &CoverUpload{Data: photo, Alt: "Test"}); err == nil {
		t.Fatal("failed transaction succeeded")
	}
	files := 0
	if err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if files != 0 {
		t.Fatalf("rollback left %d objects", files)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_event; INSERT INTO calendar_events(id,title,body,user_id,cover_key) VALUES(10,'Hidden key','Body',1,'avatars/1/source')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenCover(ctx, "10"); !errors.Is(err, ErrNotFound) {
		t.Fatal("exposed arbitrary storage key")
	}
}

func TestEventWithoutStartTimeDoesNotInventMidnight(t *testing.T) {
	s, _, _ := eventService(t)
	ctx := context.Background()
	value, err := s.Save(ctx, 1, "", Draft{Title: "LAN-Party", Body: "Uhrzeit und Ort folgen.", Date: "27.11.2026"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.Lookup(ctx, value)
	if err != nil || event.EventDate != "2026-11-27" {
		t.Fatalf("date-only event: %+v %v", event, err)
	}
}
