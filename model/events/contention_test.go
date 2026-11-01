package events

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/storage"
)

type pausedEventUpload struct {
	storage.Provider
	once             sync.Once
	started, release chan struct{}
}

func (s *pausedEventUpload) Upload(ctx context.Context, key string, input io.Reader, size int64, mime string) error {
	if err := s.Provider.Upload(ctx, key, input, size, mime); err != nil {
		return err
	}
	var err error
	s.once.Do(func() {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

func TestEventUploadsReleaseDatabaseAndRecheckAuthorization(t *testing.T) {
	for _, operation := range []string{"save", "import", "duplicate import"} {
		t.Run(operation, func(t *testing.T) {
			s, db, root := eventService(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			base := s.storage
			paused := &pausedEventUpload{Provider: base, started: make(chan struct{}), release: make(chan struct{})}
			s.storage = paused
			photo := CoverUpload{Data: eventPhoto(t, 16, 24), Alt: "Lerngruppe"}
			input := ImportedEvent{SourceURL: "https://www.instagram.com/p/Concurrent_123/", PublishedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Draft: Draft{Title: "Lerngruppe", Body: "Wir lernen zusammen für die Prüfung."}, Images: []CoverUpload{photo}}
			type result struct {
				id      string
				created bool
				err     error
			}
			finished := make(chan result, 1)
			go func() {
				if operation == "save" {
					id, err := s.Save(ctx, 1, "", input.Draft, "", &photo)
					finished <- result{id: id, err: err}
				} else {
					id, created, err := s.Import(ctx, 1, input)
					finished <- result{id, created, err}
				}
			}()
			select {
			case <-paused.started:
			case r := <-finished:
				t.Fatalf("operation failed before storage: %v", r.err)
			case <-ctx.Done():
				t.Fatal("storage not reached")
			}
			probe, stop := context.WithTimeout(context.Background(), time.Second)
			_, err := db.ExecContext(probe, "UPDATE users SET name=name WHERE id=2")
			stop()
			if err != nil {
				t.Fatal("slow upload retained SQL pool or writer lock", err)
			}
			var winner string
			if operation == "duplicate import" {
				var created bool
				winner, created, err = NewService(db, base).Import(ctx, 2, input)
				if err != nil || !created {
					t.Fatal("concurrent import failed", created, err)
				}
			} else if _, err := db.ExecContext(ctx, "UPDATE users SET active=0 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			close(paused.release)
			select {
			case r := <-finished:
				if operation == "duplicate import" {
					if r.err != nil || r.created || r.id != winner {
						t.Fatal("concurrent import was not idempotent", r)
					}
				} else if !errors.Is(r.err, ErrForbidden) {
					t.Fatal("revoked account committed event", r.err)
				}
			case <-ctx.Done():
				t.Fatal("operation did not finish")
			}
			var count int
			want := 0
			if operation == "duplicate import" {
				want = 1
			}
			if err := db.QueryRow("SELECT count(*) FROM calendar_events").Scan(&count); err != nil || count != want {
				t.Fatal("event state changed incorrectly", count, err)
			}
			if want == 0 {
				err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
					if err == nil && !entry.IsDir() {
						t.Errorf("uncommitted upload retained: %s", path)
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
