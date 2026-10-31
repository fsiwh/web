package exams

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fachschaftinformatik/web/internal/config"
	"github.com/fachschaftinformatik/web/storage/s3"
)

func TestS3DraftAuthorizationBeforeObjectAccess(t *testing.T) {
	s, db := reviewService(t)
	var requests atomic.Int32
	var corrupt atomic.Bool
	const pdf = "%PDF-1.7\nfirst\n%%EOF"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/private-exams/archive/objects/") {
			t.Errorf("unexpected storage target: %s", r.URL)
		}
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			if string(body) != pdf || r.Header.Get("Content-Type") != "application/pdf" || r.Header.Get("X-Amz-Acl") != "" {
				t.Error("invalid PDF upload or ACL")
			}
			w.Header().Set("ETag", `"pdf"`)
		case "GET":
			// The HTTP handler fixes the MIME type independently of S3 metadata.
			w.Header().Set("Content-Type", "text/html")
			if corrupt.Load() {
				io.WriteString(w, strings.Replace(pdf, "first", "wrong", 1))
			} else {
				io.WriteString(w, pdf)
			}
		default:
			t.Errorf("unexpected storage operation: %s", r.Method)
		}
	}))
	defer server.Close()
	objects, err := s3.NewClient(&config.Config{
		S3Endpoint: strings.TrimPrefix(server.URL, "http://"), S3Bucket: "private-exams",
		S3AccessKey: "test", S3SecretKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.storage = objects
	ctx := context.Background()
	input := submission(t, db)
	result, err := s.SaveDraft(ctx, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	for _, user := range []int64{0, 2, 3, 999} {
		if object, err := s.Open(ctx, user, result.IDs[0]); err == nil {
			object.Close()
			t.Errorf("user %d read another user's draft", user)
		}
	}
	input.SubmissionID = result.SubmissionID
	if _, err := s.SaveDraft(ctx, 3, input); err == nil {
		t.Error("another user overwrote draft")
	}
	if err := s.Review(ctx, 3, result.SubmissionID, true); !errors.Is(err, ErrForbidden) {
		t.Errorf("ordinary user gained approval privileges: %v", err)
	}
	if requests.Load() != before {
		t.Fatal("unauthorized operation reached S3")
	}
	object, err := s.Open(ctx, 1, result.IDs[0])
	if err != nil {
		t.Fatal(err)
	}
	object.Close()
	if err := s.Submit(ctx, 1, result.SubmissionID); err != nil {
		t.Fatal(err)
	}
	before = requests.Load()
	if _, err := s.Open(ctx, 3, result.IDs[0]); !errors.Is(err, ErrNotFound) || requests.Load() != before {
		t.Fatal("pending document exposed to ordinary reader")
	}
	if err := s.Review(ctx, 2, result.SubmissionID, true); err != nil {
		t.Fatal(err)
	}
	object, err = s.Open(ctx, 3, result.IDs[0])
	if err != nil {
		t.Fatal(err)
	}
	object.Close()
	corrupt.Store(true)
	if _, err := s.Open(ctx, 3, result.IDs[0]); !errors.Is(err, ErrStorage) {
		t.Fatalf("tampered S3 content bypassed checksum validation: %v", err)
	}
	corrupt.Store(false)
	if _, err := db.Exec(`UPDATE users SET active=0 WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	before = requests.Load()
	if _, err := s.Open(ctx, 3, result.IDs[0]); !errors.Is(err, ErrForbidden) || requests.Load() != before {
		t.Fatal("deactivated reader retained S3 access")
	}
}
