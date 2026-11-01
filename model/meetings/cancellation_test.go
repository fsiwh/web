package meetings

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/storage"
)

// Model a provider that stalls until its caller cancels, including after a
// partial upload and while reading a response body rather than just headers.
type stalledMeetingBlobs struct {
	*meetingBlobs
	stage   string
	started chan struct{}
}

func (b *stalledMeetingBlobs) Upload(ctx context.Context, key string, r io.Reader, size int64, mime string) error {
	if err := b.meetingBlobs.Upload(ctx, key, r, size, mime); err != nil {
		return err
	}
	if b.stage == "save" {
		close(b.started)
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (b *stalledMeetingBlobs) GetObject(ctx context.Context, key string) (*storage.Object, error) {
	object, err := b.meetingBlobs.GetObject(ctx, key)
	if err == nil && (b.stage == "get" || b.stage == "publish") {
		object.ReadCloser = &cancelledMeetingReader{ReadCloser: object.ReadCloser, ctx: ctx, started: b.started}
	}
	return object, err
}

func (b *stalledMeetingBlobs) Delete(ctx context.Context, key string) error {
	if b.stage == "cleanup" {
		close(b.started)
		<-ctx.Done()
		return ctx.Err()
	}
	// Compensation for a cancelled upload must use its own live context.
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.meetingBlobs.Delete(ctx, key)
}

type cancelledMeetingReader struct {
	io.ReadCloser
	ctx     context.Context
	started chan struct{}
}

func (r *cancelledMeetingReader) Read([]byte) (int, error) {
	close(r.started)
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestMeetingCancellationRollsBackAndReleasesDatabase(t *testing.T) {
	for _, stage := range []string{"save", "get", "publish", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			db, err := database.NewConnection(testdb.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(42,'meeting@example.org','Meeting','hash','editor',1,1)`); err != nil {
				t.Fatal(err)
			}
			b := &stalledMeetingBlobs{meetingBlobs: &meetingBlobs{objects: make(map[string][]byte)}, started: make(chan struct{})}
			s, err := NewStore(db, b)
			if err != nil {
				t.Fatal(err)
			}
			p := meetingDraft("cancelled-operation")
			if err := s.Save(p); err != nil {
				t.Fatal(err)
			}
			if stage == "cleanup" {
				b.objects["obsolete"] = []byte("obsolete")
				if _, err := db.Exec("INSERT INTO meeting_blob_cleanup(blob_key) VALUES('obsolete')"); err != nil {
					t.Fatal(err)
				}
			}
			b.stage = stage
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				switch stage {
				case "save":
					replacement := p
					replacement.Data = []byte("%PDF-1.4\nreplacement\n%%EOF")
					finished <- s.SaveContext(ctx, replacement)
				case "get":
					_, _, err := s.GetContext(ctx, p.ID)
					finished <- err
				case "publish":
					finished <- s.PublishContext(ctx, p.ID, p.Owner)
				case "cleanup":
					finished <- s.CleanupBlobs(ctx)
				}
			}()
			select {
			case <-b.started:
			case err := <-finished:
				t.Fatalf("operation ended before storage stalled: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("operation did not reach storage")
			}
			// External I/O must not retain even the single-slot runtime SQL pool.
			probe, stop := context.WithTimeout(context.Background(), time.Second)
			_, probeErr := db.ExecContext(probe, "UPDATE users SET name=name WHERE id=42")
			stop()
			if probeErr != nil {
				t.Fatal("stalled storage blocked unrelated database work", probeErr)
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled operation retained database lock")
			}
			b.stage = ""
			recovery, done := context.WithTimeout(context.Background(), 2*time.Second)
			defer done()
			// A new operation must acquire the one-slot pool and SQLite writer lock.
			got, ok, err := s.GetContext(recovery, p.ID)
			if err != nil || !ok || !bytes.Equal(got.Data, p.Data) || got.Published {
				t.Fatal("cancelled operation changed draft or leaked lock", got, err)
			}
			var foreignKeys, queued int
			if err := db.QueryRowContext(recovery, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
				t.Fatal("cancellation lost FK enforcement", foreignKeys, err)
			}
			if err := db.QueryRowContext(recovery, "SELECT count(*) FROM meeting_blob_cleanup").Scan(&queued); err != nil {
				t.Fatal(err)
			}
			if stage == "cleanup" {
				if queued != 1 {
					t.Fatal("cancelled deletion was not retained for retry", queued)
				}
				if err := s.CleanupBlobs(recovery); err != nil {
					t.Fatal(err)
				}
			} else if queued != 0 {
				t.Fatal("upload compensation failed", queued)
			}
			if len(b.objects) != 1 {
				t.Fatal("cancelled operation leaked objects or deleted active content", len(b.objects))
			}
		})
	}
}
