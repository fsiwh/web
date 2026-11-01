package exams

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/storage"
)

type pausedExamStorage struct {
	writableStorage
	stage            string
	started, release chan struct{}
}

func (s *pausedExamStorage) wait(ctx context.Context) error {
	close(s.started)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *pausedExamStorage) Upload(ctx context.Context, key string, input io.Reader, size int64, mime string) error {
	if err := s.writableStorage.Upload(ctx, key, input, size, mime); err != nil {
		return err
	}
	if s.stage == "upload" {
		return s.wait(ctx)
	}
	return nil
}

func (s *pausedExamStorage) GetObject(ctx context.Context, key string) (*storage.Object, error) {
	object, err := s.writableStorage.GetObject(ctx, key)
	if err == nil && s.stage == "read" {
		object.ReadCloser = &pausedExamReader{ReadCloser: object.ReadCloser, wait: func() error { return s.wait(ctx) }}
	}
	return object, err
}

func (s *pausedExamStorage) Delete(ctx context.Context, key string) error {
	if s.stage == "delete" {
		if err := s.wait(ctx); err != nil {
			return err
		}
	}
	return s.writableStorage.Delete(ctx, key)
}

type pausedExamReader struct {
	io.ReadCloser
	wait func() error
}

func (r *pausedExamReader) Read(p []byte) (int, error) {
	if r.wait != nil {
		wait := r.wait
		r.wait = nil
		if err := wait(); err != nil {
			return 0, err
		}
	}
	return r.ReadCloser.Read(p)
}

func TestSlowExamStorageAllowsDatabaseWorkAndRechecksState(t *testing.T) {
	for _, operation := range []string{"save", "edit", "open", "cleanup", "reuse"} {
		t.Run(operation, func(t *testing.T) {
			s, db := reviewService(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			input := submission(t, db)
			saved, err := s.SaveDraft(ctx, 1, input)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Submit(ctx, 1, saved.SubmissionID); err != nil {
				t.Fatal(err)
			}
			current, err := s.Submission(ctx, 2, saved.SubmissionID, true)
			if err != nil {
				t.Fatal(err)
			}
			base := s.storage.(writableStorage)
			paused := &pausedExamStorage{writableStorage: base, started: make(chan struct{}), release: make(chan struct{})}
			s.storage = paused
			if operation == "save" || operation == "edit" {
				paused.stage = "upload"
				input.Documents = [][]byte{[]byte("%PDF-1.7\nreplacement\n%%EOF")}
			} else if operation == "cleanup" {
				paused.stage = "delete"
				if _, err := db.Exec("INSERT INTO archive_blob_cleanup(blob_key) VALUES('obsolete')"); err != nil {
					t.Fatal(err)
				}
			} else {
				paused.stage = "read"
			}
			if operation == "edit" {
				input.SubmissionID = saved.SubmissionID
			}
			finished := make(chan error, 1)
			go func() {
				switch operation {
				case "save", "reuse":
					_, err := s.SaveDraft(ctx, 1, input)
					finished <- err
				case "edit":
					_, err := s.EditSubmission(ctx, 2, input, current.EditToken)
					finished <- err
				case "open":
					object, err := s.Open(ctx, 1, saved.IDs[0])
					if object != nil {
						object.Close()
					}
					finished <- err
				case "cleanup":
					finished <- s.CleanupBlobs(ctx)
				}
			}()
			select {
			case <-paused.started:
			case err := <-finished:
				t.Fatalf("operation failed before reaching storage: %v", err)
			case <-ctx.Done():
				t.Fatal("operation never reached storage")
			}
			probe, stop := context.WithTimeout(context.Background(), time.Second)
			_, probeErr := db.ExecContext(probe, "UPDATE users SET name=name WHERE id=1")
			stop()
			if probeErr != nil {
				t.Fatal("slow storage retained SQL pool or writer lock", probeErr)
			}
			want := ErrForbidden
			switch operation {
			case "save", "open":
				if _, err := db.ExecContext(ctx, "UPDATE users SET active=0 WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			case "edit":
				if err := NewService(db, base).Review(ctx, 2, saved.SubmissionID, true); err != nil {
					t.Fatal(err)
				}
				want = ErrConflict
			case "reuse":
				// Retire the very key the paused operation has already verified.
				replacement := submission(t, db)
				replacement.SubmissionID = saved.SubmissionID
				replacement.Documents = [][]byte{[]byte("%PDF-1.7\nnew target\n%%EOF")}
				if _, err := NewService(db, base).EditSubmission(ctx, 2, replacement, current.EditToken); err != nil {
					t.Fatal(err)
				}
				want = ErrConflict
			case "cleanup":
				cancel()
				want = context.Canceled
			}
			close(paused.release)
			select {
			case err := <-finished:
				if !errors.Is(err, want) {
					t.Fatalf("state change during storage was ignored: %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("operation did not release database")
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM archive_requests").Scan(&count); err != nil || count != 1 {
				t.Fatal("stale operation created a submission", count, err)
			}
			if operation == "cleanup" {
				if err := db.QueryRow("SELECT count(*) FROM archive_blob_cleanup WHERE blob_key='obsolete'").Scan(&count); err != nil || count != 1 {
					t.Fatal("cancelled cleanup lost retry", count, err)
				}
			}
		})
	}
}
