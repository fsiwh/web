package exams

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/storage/disk"
)

func examsService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	path := testdb.Path(t)
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
INSERT INTO programs (id,name) VALUES (1,'Informatik (B. Sc.)'),(2,'Informatik (M. Sc.)');
INSERT INTO program_versions (program_id,name) VALUES (1,'2017'),(1,'2023'),(2,'2023');
INSERT INTO modules (id,program_id,name,alias) VALUES (10,1,'Datenbanken','SQL'),(11,2,'Datenbanken II',NULL),(12,1,'Ohne Upload',NULL);
INSERT INTO users (id,email,name,password,active,verified) VALUES (1,'archive@council.example.invalid','Person','unused',1,1);
INSERT INTO archive (id,group_id,user_id,module_id,version,exam_date,is_latest,access_key,mime_type,nbytes,checksum) VALUES
(100,100,1,10,'2017','2024-01-01',0,'old','application/pdf',5,'checksum'),
(101,100,1,10,'2017','2024-01-01',1,'latest','application/pdf',5,'checksum'),
(102,102,1,10,'2023','2026-01-01',1,'new','application/pdf',5,'checksum'),
(103,103,1,11,'2023','2025-01-01',1,'master','application/pdf',5,'checksum');`)
	if err != nil {
		t.Fatal(err)
	}
	seedPublicIDs(t, db)
	storage, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storage.Close() })
	for _, value := range []string{"100", "101", "102", "103"} {
		if err := storage.Upload(context.Background(), "archive/"+value+"/source.pdf", strings.NewReader("%PDF-"), 5, "application/pdf"); err != nil {
			t.Fatal(err)
		}
	}
	return NewService(db, storage), db
}

func seedPublicIDs(t *testing.T, db *sql.DB) {
	t.Helper()
	for namespace, table := range map[string]string{"modules": "modules", "programs": "programs", "documents": "archive", "submissions": "archive_requests", "users": "users"} {
		rows, err := db.Query("SELECT CAST(id AS TEXT) FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if _, err := publicid.Ensure(context.Background(), db, namespace, id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func testPublicID(t *testing.T, db *sql.DB, namespace, id string) string {
	t.Helper()
	value, err := publicid.Lookup(context.Background(), db, namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testInternalID(t *testing.T, db *sql.DB, namespace, id string) string {
	t.Helper()
	value, err := publicid.Resolve(context.Background(), db, namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPersistentCatalogLatestRevisionsAndFilters(t *testing.T) {
	s, db := examsService(t)
	catalog, err := s.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	modules := catalog.FindModules(Filter{})
	if len(modules) != 2 || modules[0].ID != testPublicID(t, db, "modules", "10") || len(modules[0].Exams) != 2 || modules[0].Exams[0].ID != testPublicID(t, db, "documents", "102") || modules[0].Exams[1].ID != testPublicID(t, db, "documents", "101") {
		t.Fatalf("latest grouped uploads: %+v", modules)
	}
	for _, tc := range []struct {
		filter Filter
		count  int
	}{
		{Filter{Query: " SQL "}, 2},
		{Filter{Programs: []string{testPublicID(t, db, "programs", "2")}, Degrees: []string{"master"}}, 1},
		{Filter{Degrees: []string{"bachelor", "master"}, Versions: []string{"2017", "2023"}}, 3},
		{Filter{Degrees: []string{"master"}, Versions: []string{"2017"}}, 0},
	} {
		if got := len(catalog.FindExams(tc.filter)); got != tc.count {
			t.Fatalf("filter %+v: got %d want %d", tc.filter, got, tc.count)
		}
	}
	if module, ok := catalog.FindModule(testPublicID(t, db, "modules", "12"), Filter{}); !ok || len(module.Exams) != 0 {
		t.Fatal("empty existing module lost its URL")
	}
	if _, err := db.Exec(`UPDATE programs SET deleted_at='2026-01-01T00:00:00Z' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	catalog, err = s.Catalog(context.Background())
	if err != nil || len(catalog.FindExams(Filter{})) != 1 {
		t.Fatal("deleted programme exposed exam documents")
	}
}

func TestPersistentDownloadsRetainHistoricalKeysAndCheckMetadata(t *testing.T) {
	s, db := examsService(t)
	ctx := context.Background()
	object, err := s.Open(ctx, 1, testPublicID(t, db, "documents", "100"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(value) != "%PDF-" {
		t.Fatal("historical revision or V1 object key lost")
	}
	if _, err := db.Exec(`UPDATE archive SET nbytes=100 WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, 1, testPublicID(t, db, "documents", "101")); !errors.Is(err, ErrStorage) {
		t.Fatal("mismatching storage metadata accepted")
	}
	if _, err := db.Exec(`UPDATE archive SET deleted_at='2026-01-01T00:00:00Z' WHERE id=100`); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"100", "999", "../100", "0"} {
		if _, err := s.Open(ctx, 1, value); !errors.Is(err, ErrNotFound) {
			t.Fatalf("download %q: %v", value, err)
		}
	}
}
