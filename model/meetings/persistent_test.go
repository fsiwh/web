package meetings

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/content"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/storage"
)

type meetingBlobs struct {
	sync.Mutex
	objects     map[string][]byte
	fail        bool
	failDelete  bool
	unavailable bool
	gets        int
	uploads     int
	readStarted chan struct{}
	readRelease chan struct{}
}

func (b *meetingBlobs) EnsureBucket(context.Context) error               { return nil }
func (b *meetingBlobs) CopyObject(context.Context, string, string) error { return errors.New("unused") }
func (b *meetingBlobs) Upload(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b.Lock()
	defer b.Unlock()
	value, err := io.ReadAll(r)
	b.objects[key] = value
	b.uploads++
	if b.fail {
		return errors.New("upload failed after writing")
	}
	return err
}
func (b *meetingBlobs) Delete(_ context.Context, key string) error {
	b.Lock()
	defer b.Unlock()
	if b.failDelete {
		return errors.New("delete unavailable")
	}
	delete(b.objects, key)
	return nil
}
func (b *meetingBlobs) GetObject(_ context.Context, key string) (*storage.Object, error) {
	b.Lock()
	defer b.Unlock()
	b.gets++
	if b.unavailable {
		return nil, errors.New("blob unavailable")
	}
	value, ok := b.objects[key]
	if !ok {
		return nil, errors.New("missing blob")
	}
	var reader io.ReadCloser = io.NopCloser(bytes.NewReader(value))
	if b.readStarted != nil {
		reader = &meetingReadBarrier{Reader: bytes.NewReader(value), started: b.readStarted, release: b.readRelease}
		b.readStarted = nil
	}
	return &storage.Object{ReadCloser: reader, Info: storage.ObjectInfo{Size: int64(len(value))}}, nil
}

type meetingReadBarrier struct {
	*bytes.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *meetingReadBarrier) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.release })
	return r.Reader.Read(p)
}
func (r *meetingReadBarrier) Close() error { return nil }

func persistentFixture(t *testing.T) (*Store, *meetingBlobs, func() *Store) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "meetings.sqlite") + "?_pragma=busy_timeout(5000)"
	if err := database.Migrate(dsn, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	seed, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = seed.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES
		(1,'first@example.org','First','hash','admin',1,1),
		(42,'owner@example.org','Owner','hash','editor',1,1),
		(43,'other@example.org','Other','hash','editor',1,1)`)
	seed.Close()
	if err != nil {
		t.Fatal(err)
	}
	b := &meetingBlobs{objects: make(map[string][]byte)}
	open := func() *Store {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		s, err := NewStore(db, b)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	return open(), b, open
}
func meetingDraft(id string) Publication {
	return Publication{ID: id, Owner: "42", Date: "2028-10-28", Number: 45, Kind: "Einladung", Filename: "sitzung.pdf", Data: []byte("%PDF-1.4\nmeeting\n%%EOF")}
}

func TestPersistentMeetingIndexUsesOnlyMetadata(t *testing.T) {
	s, b, _ := persistentFixture(t)
	p := meetingDraft("published")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(p.ID, p.Owner); err != nil {
		t.Fatal(err)
	}
	gets := b.gets
	b.unavailable = true
	all, err := s.AllWithError()
	if err != nil {
		t.Fatal("index depended on blob availability", err)
	}
	doc := all[0].Documents[0]
	if doc.File != p.ID || doc.Bytes != len(p.Data) || b.gets != gets {
		t.Fatalf("incorrect metadata index: %+v, blob reads %d", doc, b.gets-gets)
	}
	if _, _, err := s.GetWithError(p.ID); err == nil {
		t.Fatal("download hid unavailable blob")
	}
	q := meetingDraft("draft")
	if err := s.Save(q); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(q.ID, q.Owner); err == nil {
		t.Fatal("publish skipped blob verification")
	}
}

func TestPersistentMeetingReusesContentAndRetriesCleanup(t *testing.T) {
	s, b, reopen := persistentFixture(t)
	p := meetingDraft("draft")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	p.Filename = "renamed.pdf"
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if b.uploads != 1 || len(b.objects) != 1 {
		t.Fatal("metadata edit uploaded duplicate content")
	}
	b.failDelete = true
	p.Data = []byte("%PDF-1.4\nreplacement\n%%EOF")
	if err := s.Save(p); err != nil {
		t.Fatal("cleanup failure invalidated committed draft", err)
	}
	var queued int
	if err := s.db.QueryRow("SELECT count(*) FROM meeting_blob_cleanup").Scan(&queued); err != nil || queued != 1 {
		t.Fatal("superseded blob not queued", queued, err)
	}
	got, ok, err := s.GetWithError(p.ID)
	if err != nil || !ok || !bytes.Equal(got.Data, p.Data) {
		t.Fatal("replacement not committed", err)
	}
	s = reopen()
	if err := s.CleanupBlobs(context.Background()); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	b.failDelete = false
	if err := s.CleanupBlobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.objects) != 1 {
		t.Fatal("obsolete draft blob retained after retry")
	}
	if err := s.Publish(p.ID, p.Owner); err != nil {
		t.Fatal(err)
	}
	// Even an erroneous queue entry must never remove an active publication.
	if _, err := s.db.Exec("INSERT INTO meeting_blob_cleanup(blob_key) SELECT blob_key FROM meeting_publications WHERE id=?", p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupBlobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetWithError(p.ID); err != nil {
		t.Fatal("cleanup deleted active publication", err)
	}
}

func TestPersistentMeetingReplacementDoesNotBlockDownloadSnapshot(t *testing.T) {
	s, b, reopen := persistentFixture(t)
	p := meetingDraft("draft")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	other := reopen()
	started, release := make(chan struct{}), make(chan struct{})
	b.readStarted, b.readRelease = started, release
	read := make(chan error, 1)
	go func() {
		got, _, err := s.GetWithError(p.ID)
		if err == nil && !bytes.Equal(got.Data, p.Data) {
			err = errors.New("snapshot changed")
		}
		read <- err
	}()
	<-started
	replacement := p
	replacement.Data = []byte("%PDF-1.4\nreplacement\n%%EOF")
	saved := make(chan error, 1)
	go func() { saved <- other.Save(replacement) }()
	select {
	case err := <-saved:
		if err != nil {
			close(release)
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("slow download blocked an independent save")
	}
	close(release)
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	if len(b.objects) != 1 {
		t.Fatal("superseded blob was not cleaned")
	}
}

func TestPersistentMeetingPublishRejectsConcurrentReplacement(t *testing.T) {
	s, b, reopen := persistentFixture(t)
	p := meetingDraft("draft")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	other := reopen()
	started, release := make(chan struct{}), make(chan struct{})
	b.readStarted, b.readRelease = started, release
	finished := make(chan error, 1)
	go func() { finished <- s.Publish(p.ID, p.Owner) }()
	<-started
	p.Data = []byte("%PDF-1.4\nreplacement\n%%EOF")
	if err := other.Save(p); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; err == nil {
		t.Fatal("published bytes that were not verified by this operation")
	}
	got, ok, err := s.GetWithError(p.ID)
	if err != nil || !ok || got.Published || !bytes.Equal(got.Data, p.Data) {
		t.Fatal("concurrent publication changed replacement", got, err)
	}
}

func TestPersistentMeetingPublishRechecksRevokedAccount(t *testing.T) {
	s, b, _ := persistentFixture(t)
	p := meetingDraft("draft")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	b.readStarted, b.readRelease = started, release
	finished := make(chan error, 1)
	go func() { finished <- s.Publish(p.ID, p.Owner) }()
	<-started
	if _, err := s.db.Exec("UPDATE users SET active=0 WHERE id=42"); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; !errors.Is(err, content.ErrForbidden) {
		t.Fatal("revoked account published a draft", err)
	}
	got, ok, err := s.GetWithError(p.ID)
	if err != nil || !ok || got.Published {
		t.Fatal("revoked publication changed draft", got, err)
	}
}

type revokingMeetingUpload struct {
	storage.Provider
	revoke func() error
}

func (s revokingMeetingUpload) Upload(ctx context.Context, key string, input io.Reader, size int64, mime string) error {
	if err := s.Provider.Upload(ctx, key, input, size, mime); err != nil {
		return err
	}
	return s.revoke()
}

func TestPersistentMeetingSaveRechecksRevokedAccount(t *testing.T) {
	s, b, _ := persistentFixture(t)
	s.blobs = revokingMeetingUpload{Provider: b, revoke: func() error {
		_, err := s.db.Exec("UPDATE users SET role='user' WHERE id=42")
		return err
	}}
	if err := s.Save(meetingDraft("draft")); !errors.Is(err, content.ErrForbidden) {
		t.Fatal("demoted account saved a draft", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM meeting_publications").Scan(&count); err != nil || count != 0 || len(b.objects) != 0 {
		t.Fatal("revoked upload left content behind", count, err)
	}
}

func TestPersistentMeetingFailedUploadCleanupSurvivesRestart(t *testing.T) {
	s, b, reopen := persistentFixture(t)
	p := meetingDraft("draft")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), p.Data...)
	b.fail, b.failDelete = true, true
	p.Data = []byte("%PDF-1.4\nfailed replacement\n%%EOF")
	if err := s.Save(p); err == nil {
		t.Fatal("failed upload reported success")
	}
	var queued int
	if err := s.db.QueryRow("SELECT count(*) FROM meeting_blob_cleanup").Scan(&queued); err != nil || queued != 1 {
		t.Fatal("failed compensation was not queued", queued, err)
	}
	s = reopen()
	b.fail, b.failDelete = false, false
	if err := s.CleanupBlobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetWithError(p.ID)
	if err != nil || !ok || !bytes.Equal(got.Data, original) || len(b.objects) != 1 {
		t.Fatal("cleanup damaged original draft", err)
	}
}

func TestPersistentMeetingRestartAndOwnership(t *testing.T) {
	s, _, reopen := persistentFixture(t)
	p := meetingDraft("first")
	// A caller cannot bypass the separate publication step.
	p.Published = true
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s = reopen()
	got, ok, err := s.GetWithError(p.ID)
	if err != nil || !ok || got.Published || !bytes.Equal(got.Data, p.Data) {
		t.Fatalf("draft lost: %+v %v", got, err)
	}
	p.Owner = "43"
	if err := s.Save(p); err == nil {
		t.Fatal("foreign owner replaced draft")
	}
	if err := s.Publish(p.ID, p.Owner); err == nil {
		t.Fatal("foreign owner published draft")
	}
	before, err := s.AllWithError()
	if err != nil || len(before) != 0 {
		t.Fatal("draft became public", err)
	}
	if err := s.Publish(p.ID, "42"); err != nil {
		t.Fatal(err)
	}
	s = reopen()
	if err := s.Publish(p.ID, "42"); err != nil {
		t.Fatal("publication is not idempotent", err)
	}
	after, err := s.AllWithError()
	if err != nil || len(after) != 1 || after[0].Documents[0].File != "first" || !after[0].Documents[0].Stored {
		t.Fatal("publication not durable", err)
	}
	p.Owner = "42"
	if err := s.Save(p); err == nil {
		t.Fatal("published document changed")
	}
}

func TestPersistentMeetingFailureCompensationAndIntegrity(t *testing.T) {
	s, b, _ := persistentFixture(t)
	p := meetingDraft("draft")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	original := len(b.objects)
	b.fail = true
	p.Data = []byte("%PDF-1.4\nreplacement\n%%EOF")
	if err := s.Save(p); err == nil {
		t.Fatal("upload failure hidden")
	}
	if len(b.objects) != original {
		t.Fatal("failed upload not removed")
	}
	got, _, err := s.GetWithError(p.ID)
	if err != nil || bytes.Equal(got.Data, p.Data) {
		t.Fatal("failed upload replaced original", err)
	}
	b.fail = false
	if _, err := s.db.Exec(`CREATE TRIGGER reject_meeting BEFORE UPDATE ON meeting_publications WHEN NEW.filename='reject.pdf' BEGIN SELECT RAISE(ABORT,'rejected'); END`); err != nil {
		t.Fatal(err)
	}
	p.Filename = "reject.pdf"
	if err := s.Save(p); err == nil {
		t.Fatal("SQL failure hidden")
	}
	if len(b.objects) != original {
		t.Fatal("SQL rollback leaked new blob")
	}
	for key := range b.objects {
		b.objects[key] = []byte("corrupt")
	}
	if _, _, err := s.GetWithError(p.ID); err == nil {
		t.Fatal("corrupt blob accepted")
	}
	if err := s.Publish(p.ID, p.Owner); err == nil {
		t.Fatal("corrupt blob published")
	}
}

func TestPersistentMeetingConcurrentIdentity(t *testing.T) {
	s, _, reopen := persistentFixture(t)
	other := reopen()
	p, q := meetingDraft("a"), meetingDraft("b")
	q.Date = "2028-10-29"
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := other.Save(q); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- s.Publish(p.ID, p.Owner) }()
	go func() { <-start; results <- other.Publish(q.ID, q.Owner) }()
	close(start)
	a, b := <-results, <-results
	if (a == nil) == (b == nil) {
		t.Fatalf("expected exactly one publication: %v, %v", a, b)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM meeting_publications WHERE published=1").Scan(&count); err != nil || count != 1 {
		t.Fatal("conflicting publications committed", count, err)
	}
}

func TestPersistentMeetingCatalogAndMatchingDocuments(t *testing.T) {
	s, _, _ := persistentFixture(t)
	p := meetingDraft("invitation")
	p.Owner = "raw-session-credential"
	if err := s.Save(p); err == nil {
		t.Fatal("session credential accepted as persistent owner")
	}
	p.Owner = "42"
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(p.ID, p.Owner); err != nil {
		t.Fatal(err)
	}
	p.ID, p.Kind = "minutes", "Protokoll"
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(p.ID, p.Owner); err != nil {
		t.Fatal(err)
	}
	all, err := s.AllWithError()
	if err != nil || len(all[0].Documents) != 2 {
		t.Fatal("matching documents not grouped", err)
	}
	p.ID, p.Number = "dateconflict", 46
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(p.ID, p.Owner); err == nil {
		t.Fatal("same date accepted for another number")
	}
	if _, err := s.db.Exec(`INSERT INTO historical_meeting_documents(file,year,number,date,kind,note,bytes,sha256,sources,blob_key) VALUES('old.pdf',2020,1,'2020-01-02','Einladung','',1,'hash','[]','historical')`); err != nil {
		t.Fatal(err)
	}
	historical := Meeting{Date: "2020-01-02", Number: 1}
	p.ID, p.Date, p.Number = "historical", historical.Date, historical.Number+1
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(p.ID, p.Owner); err == nil {
		t.Fatal("curated meeting identity overwritten")
	}
}
