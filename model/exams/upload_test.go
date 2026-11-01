package exams

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strconv"
	"testing"
)

func submission(t *testing.T, db *sql.DB) SubmissionDraft {
	return SubmissionDraft{ModuleID: testPublicID(t, db, "modules", "10"), Version: "2023", Date: "2026-02-12", Documents: [][]byte{[]byte("%PDF-1.7\nfirst\n%%EOF")}}
}

func TestMultiplePersistentUploadsAndAuthorizedRevision(t *testing.T) {
	s, db := examsService(t)
	if _, err := db.Exec(`UPDATE users SET active=1,verified=1,role='admin' WHERE id=1; INSERT INTO users(id,email,name,password,active,verified) VALUES (2,'other@council.example.invalid','Other','unused',1,1);`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result, err := s.SaveDraft(ctx, 1, submission(t, db))
	if err != nil || len(result.IDs) != 1 {
		t.Fatalf("single upload: %+v %v", result, err)
	}
	var groups int
	if err := s.Submit(ctx, 1, result.SubmissionID); err != nil {
		t.Fatal(err)
	}
	if err := s.Review(ctx, 1, result.SubmissionID, true); err != nil {
		t.Fatal(err)
	}
	second := submission(t, db)
	second.Documents = [][]byte{[]byte("%PDF-1.7\nsecond\n%%EOF")}
	other, err := s.SaveDraft(ctx, 1, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(ctx, 1, other.SubmissionID); err != nil {
		t.Fatal(err)
	}
	if err := s.Review(ctx, 1, other.SubmissionID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(DISTINCT group_id) FROM archive WHERE exam_date='2026-02-12' AND is_latest=1`).Scan(&groups); err != nil || groups != 2 {
		t.Fatal("independent PDFs were treated as revisions of one another")
	}
	restarted := NewService(db, s.storage)
	for _, id := range result.IDs {
		object, err := restarted.Open(ctx, 1, id)
		if err != nil {
			t.Fatal(err)
		}
		object.Close()
	}
	revision := submission(t, db)
	revision.Documents = revision.Documents[:1]
	revision.RevisionOf = result.IDs[0]
	if _, err := s.SaveDraft(ctx, 2, revision); !errors.Is(err, ErrForbidden) {
		t.Fatal("another user revised a reconstruction")
	}
	if _, err := db.Exec(`UPDATE users SET role='editor' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	updated, err := s.SaveDraft(ctx, 2, revision)
	if err != nil {
		t.Fatal(err)
	}
	var latest, version int
	if err := s.Submit(ctx, 2, updated.SubmissionID); err != nil {
		t.Fatal(err)
	}
	if err := s.Review(ctx, 1, updated.SubmissionID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT is_latest FROM archive WHERE id=?`, testInternalID(t, db, "documents", result.IDs[0])).Scan(&latest); err != nil || latest != 0 {
		t.Fatal("old revision remained current")
	}
	if err := db.QueryRow(`SELECT edit_version FROM archive WHERE id=? AND is_latest=1`, testInternalID(t, db, "documents", updated.IDs[0])).Scan(&version); err != nil || version != 2 {
		t.Fatal("revision sequence not retained")
	}
	if _, err := s.Revision(ctx, 1, updated.IDs[0]); err != nil {
		t.Fatal("original owner lost revision rights after an editor upload")
	}
}

type failingStorage struct {
	writableStorage
	failAt, calls int
	deleted       []string
}

func (s *failingStorage) Upload(ctx context.Context, key string, input io.Reader, size int64, contentType string) error {
	s.calls++
	if s.calls == s.failAt {
		return errors.New("object store unavailable")
	}
	return s.writableStorage.Upload(ctx, key, input, size, contentType)
}

func (s *failingStorage) Delete(ctx context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	return s.writableStorage.Delete(ctx, key)
}

func TestFailedUploadOrCommitRetainsPreviousRevision(t *testing.T) {
	for _, mode := range []string{"storage", "database"} {
		t.Run(mode, func(t *testing.T) {
			s, db := examsService(t)
			if _, err := db.Exec(`UPDATE users SET active=1,verified=1 WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			storage := &failingStorage{writableStorage: s.storage.(writableStorage)}
			s.storage = storage
			input := submission(t, db)
			if mode == "storage" {
				storage.failAt = 1
			} else {
				input.RevisionOf = testPublicID(t, db, "documents", "101")
				input.Documents = input.Documents[:1]
				if _, err := db.Exec(`CREATE TRIGGER fail_archive BEFORE INSERT ON archive BEGIN SELECT RAISE(FAIL,'forced write failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.SaveDraft(context.Background(), 1, input); err == nil {
				t.Fatal("failure not reported")
			}
			var count, latest int
			if err := db.QueryRow(`SELECT COUNT(*) FROM archive`).Scan(&count); err != nil || count != 4 {
				t.Fatal("failed batch committed partial records")
			}
			if err := db.QueryRow(`SELECT is_latest FROM archive WHERE id=101`).Scan(&latest); err != nil || latest != 1 {
				t.Fatal("failed revision removed current file")
			}
			if len(storage.deleted) != storage.calls {
				t.Fatal("uncommitted objects not cleaned up")
			}
			for _, key := range storage.deleted {
				if object, err := storage.GetObject(context.Background(), key); err == nil {
					object.Close()
					t.Fatal("orphaned upload remains")
				}
			}
		})
	}
}

func TestUploadValidationDoesNotWriteObjects(t *testing.T) {
	s, db := examsService(t)
	if _, err := db.Exec(`UPDATE users SET active=1,verified=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	storage := &failingStorage{writableStorage: s.storage.(writableStorage)}
	s.storage = storage
	for index, change := range []func(*SubmissionDraft){
		func(s *SubmissionDraft) { s.Version = "missing" },
		func(s *SubmissionDraft) { s.ModuleID = "999" },
		func(s *SubmissionDraft) { s.Documents[0] = []byte("not a PDF") },
		func(s *SubmissionDraft) { s.Documents[0] = make([]byte, 8<<20+1) },
		func(s *SubmissionDraft) { s.Date = "not a date" },
		func(s *SubmissionDraft) { s.Documents = append(s.Documents, s.Documents[0]) },
	} {
		input := submission(t, db)
		change(&input)
		_, err := s.SaveDraft(context.Background(), 1, input)
		var fields ValidationErrors
		if !errors.As(err, &fields) || storage.calls != 0 {
			t.Fatalf("invalid input %s wrote storage: %v", strconv.Itoa(index), err)
		}
	}
}
