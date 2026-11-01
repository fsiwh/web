package media

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	objectstore "github.com/fachschaftinformatik/web/storage"
	"github.com/fachschaftinformatik/web/storage/disk"
)

func mediaService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sqlite.db")
	if err := database.Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`INSERT INTO users (id,email,name,password,private) VALUES (1,'media@council.example.invalid','Private uploader','unused',1);
INSERT INTO events (id,title,created_at,deleted_at) VALUES (10,'Sommerfest','2025-01-01T00:00:00Z',NULL),(20,'LAN','2026-01-01T00:00:00Z',NULL),(30,'Deleted','2027-01-01T00:00:00Z','2027-01-02T00:00:00Z'),(40,'Empty','2024-01-01T00:00:00Z',NULL);
INSERT INTO media (id,event_id,user_id,title,description,access_key,mime_type,nbytes,created_at,deleted_at) VALUES
(99,10,1,'Older','Older photograph','older','image/jpeg',5,'2025-01-01T00:00:00Z',NULL),
(100,10,1,'Echter Titel','Echter Alternativtext','not-a-blob-path','image/jpeg',5,'2026-01-01T00:00:00Z',NULL),
(101,10,1,NULL,NULL,'fallback','image/jpeg',5,'2026-01-01T00:00:00Z',NULL),
(102,20,1,'New','Description','new','image/jpeg',5,'2026-01-02T00:00:00Z',NULL),
(103,20,1,'Deleted','Hidden','deleted','image/jpeg',5,'2026-01-03T00:00:00Z','2026-01-04T00:00:00Z'),
(104,30,1,'Deleted album','Hidden','deleted-album','image/jpeg',5,'2026-01-01T00:00:00Z',NULL),
(105,20,1,'SVG','Hidden','svg','image/svg+xml',5,'2026-01-01T00:00:00Z',NULL),
(106,20,1,'HTML','Hidden','html','text/html',5,'2026-01-01T00:00:00Z',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE media SET homepage_visible=1`); err != nil {
		t.Fatal(err)
	}
	storage, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	for _, id := range []string{"100", "101", "102", "103", "104", "105", "106"} {
		for _, key := range []string{objectstore.MediaSourceKey(id), objectstore.MediaPreviewKey(id, 400)} {
			if err := storage.Upload(context.Background(), key, strings.NewReader("photo"), 5, "image/jpeg"); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedReferences(t, db)
	return NewService(db, storage), db
}

func TestCatalogCompleteAndFiltered(t *testing.T) {
	s, db := mediaService(t)
	// Exceed several pages: every eligible row must reach the catalog.
	for id := 200; id < 273; id++ {
		_, err := db.Exec(`INSERT INTO media (id,event_id,user_id,access_key,mime_type,nbytes,created_at,homepage_visible) VALUES (?,20,1,?,'image/png',5,'2026-01-01T00:00:00Z',1)`, id, strconv.Itoa(id))
		if err != nil {
			t.Fatal(err)
		}
	}
	seedReferences(t, db)
	albums, err := s.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 2 || albums[0].ID != reference(t, db, "albums", "20") || albums[1].ID != reference(t, db, "albums", "10") {
		t.Fatalf("albums: %+v", albums)
	}
	if len(albums[0].Images) != 74 || albums[0].OriginalCount != 74 || len(albums[1].Images) != 3 {
		t.Fatalf("unexpected image counts: %+v", albums)
	}
	for i, image := range albums[0].Images {
		if i > 0 && image.ID != reference(t, db, "media", strconv.Itoa(273-i)) {
			t.Errorf("image order at %d: %s", i, image.ID)
		}
		if image.Title == "" || image.Description == "" || image.AlbumCover {
			t.Errorf("image metadata: %+v", image)
		}
	}
	actual := albums[1].Images[1]
	if actual.ID != reference(t, db, "media", "100") || actual.Title != "Echter Titel" || actual.Description != "Echter Alternativtext" {
		t.Errorf("lost real metadata: %+v", actual)
	}
	if fallback := albums[1].Images[0]; fallback.Title != "Sommerfest – Bild 1" || fallback.Description != fallback.Title {
		t.Errorf("fallback: %+v", fallback)
	}
}

func TestEventCoversUseOnlyTheirOwnRegisteredPreview(t *testing.T) {
	s, db := mediaService(t)
	_, err := db.Exec(`UPDATE events SET cover_path=CASE id WHEN 10 THEN 'events/10/cover/source' WHEN 20 THEN 'avatars/1/source' WHEN 30 THEN 'events/30/cover/source' WHEN 40 THEN 'events/40/cover/source' END`)
	if err != nil {
		t.Fatal(err)
	}
	key := objectstore.AlbumCoverPreviewKey("10", 400)
	if err := s.storage.Upload(context.Background(), key, strings.NewReader("cover"), 5, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	object, err := s.OpenCover(context.Background(), reference(t, db, "albums", "10"), "400")
	if err != nil {
		t.Fatal(err)
	}
	value, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(value) != "cover" || object.Info.ContentType != "image/jpeg" {
		t.Fatalf("cover bytes or type: %q, %v", value, err)
	}
	for _, tc := range []struct{ id, size string }{{"20", "400"}, {"30", "400"}, {"999", "400"}, {"010", "400"}, {"../10", "400"}, {"10", "original"}, {"10", "../source"}} {
		if tc.id == "10" || tc.id == "20" || tc.id == "30" {
			tc.id = reference(t, db, "albums", tc.id)
		}
		if _, err := s.OpenCover(context.Background(), tc.id, tc.size); !errors.Is(err, ErrNotFound) {
			t.Errorf("unregistered cover %s/%s: %v", tc.id, tc.size, err)
		}
	}
	albums, err := s.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 2 || !albums[1].Cover.AlbumCover || albums[1].Cover.ID != reference(t, db, "albums", "10") {
		t.Fatalf("configured covers and fallback: %+v", albums)
	}
}

func TestOpenHistoricalKeysAndProtections(t *testing.T) {
	s, db := mediaService(t)
	ctx := context.Background()
	for _, size := range []string{"original", "400"} {
		object, err := s.Open(ctx, reference(t, db, "media", "100"), size)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(object)
		object.Close()
		if err != nil || string(body) != "photo" || object.Info.ContentType != "image/jpeg" || object.Info.Size != 5 {
			t.Fatalf("object: %+v, %q, %v", object.Info, body, err)
		}
	}
	for _, id := range []string{"100", "103", "104", "105", "106", "999", "../avatars/1/source", "avatars", "01", "+100", "0", "-1"} {
		if id == "103" || id == "104" || id == "105" || id == "106" {
			id = reference(t, db, "media", id)
		}
		if object, err := s.Open(ctx, id, "original"); !errors.Is(err, ErrNotFound) || object != nil {
			t.Errorf("id %q: object %v, error %v", id, object, err)
		}
	}
	for _, size := range []string{"source", "401", "0400", "../source", "400.jpg", ""} {
		if _, err := s.Open(ctx, reference(t, db, "media", "100"), size); !errors.Is(err, ErrNotFound) {
			t.Errorf("size %q: %v", size, err)
		}
	}
	if _, err := s.Open(ctx, reference(t, db, "media", "100"), "800"); !errors.Is(err, ErrStorage) {
		t.Errorf("missing preview: %v", err)
	}
	if _, err := db.Exec(`UPDATE media SET nbytes=6 WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, reference(t, db, "media", "100"), "original"); !errors.Is(err, ErrStorage) {
		t.Errorf("mismatched source: %v", err)
	}
}

// snapshotStorage exposes only read access to the optional supplied production
// mirror. It deliberately does not construct a writable disk provider.
type snapshotStorage struct {
	objectstore.Provider
	root string
}

func (s snapshotStorage) GetObject(ctx context.Context, key string) (*objectstore.Object, error) {
	file, err := os.Open(filepath.Join(s.root, filepath.FromSlash(key)))
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &objectstore.Object{ReadCloser: file, Info: objectstore.ObjectInfo{Size: stat.Size()}}, nil
}

func TestProductionSnapshotCatalogAndBlobIntegrity(t *testing.T) {
	root := os.Getenv("MEDIA_SNAPSHOT")
	if root == "" {
		t.Skip("set MEDIA_SNAPSHOT to the supplied read-only production mirror")
	}
	fixture, err := os.ReadFile(filepath.Join(root, "sqlite.db"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sqlite.db")
	if err := os.WriteFile(path, fixture, 0600); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewService(db, snapshotStorage{root: filepath.Join(root, "bucket")})
	albums, err := s.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 3 {
		t.Fatalf("albums: %d", len(albums))
	}
	total := 0
	for _, album := range albums {
		total += len(album.Images)
		for _, photo := range album.Images {
			for _, size := range []string{"original", "200", "400", "600", "800", "1200", "1600"} {
				object, err := s.Open(context.Background(), photo.ID, size)
				if err != nil {
					t.Fatalf("%s/%s: %v", photo.ID, size, err)
				}
				body, err := io.ReadAll(object)
				object.Close()
				if err != nil || int64(len(body)) != object.Info.Size {
					t.Fatalf("%s/%s: incomplete blob: %v", photo.ID, size, err)
				}
				cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
				if err != nil || cfg.Width < 1 || cfg.Height < 1 {
					t.Fatalf("%s/%s: invalid image: %v", photo.ID, size, err)
				}
				if size != "original" {
					pixels, _ := strconv.Atoi(size)
					if format != "jpeg" || max(cfg.Width, cfg.Height) > pixels {
						t.Fatalf("%s/%s: invalid preview: %+v (%s)", photo.ID, size, cfg, format)
					}
					if _, err := jpeg.Decode(bytes.NewReader(body)); err != nil {
						t.Fatalf("%s/%s: damaged JPEG: %v", photo.ID, size, err)
					}
				}
			}
		}
	}
	if total != 228 {
		t.Fatalf("catalog has %d images, want all 228", total)
	}
}
