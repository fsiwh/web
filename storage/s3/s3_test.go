package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/config"
	"github.com/fachschaftinformatik/web/storage"
)

// Exercise the real SDK against an S3-compatible HTTP endpoint, including signing,
// wire-level metadata and error translation rather than mocking SDK methods.
func TestObjectOperations(t *testing.T) {
	const key = "folder/a b+ü.txt"
	modified := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		operations = append(operations, r.Method+" "+r.URL.Path)
		if !strings.Contains(r.Header.Get("Authorization"), "/eu-central-1/s3/aws4_request") {
			t.Error("missing regional SigV4 signature")
		}
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			if r.URL.Path == "/bucket" {
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "<LocationConstraint>eu-central-1</LocationConstraint>") {
					t.Errorf("missing bucket region: %s", body)
				}
				return
			}
			if source := r.Header.Get("X-Amz-Copy-Source"); source != "" {
				if source != "bucket%2Ffolder%2Fa%20b%2B%C3%BC.txt" {
					t.Errorf("incorrect copy source: %s", source)
				}
				fmt.Fprint(w, `<CopyObjectResult><ETag>"copied"</ETag></CopyObjectResult>`)
				return
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "hello" || r.Header.Get("Content-Type") != "text/plain" || r.ContentLength != 5 {
				t.Errorf("unexpected upload: %q, headers %v", body, r.Header)
			}
			w.Header().Set("ETag", `"abc"`)
		case http.MethodGet:
			if r.URL.Path == "/bucket/missing" {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("ETag", `"abc"`)
			w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
			fmt.Fprint(w, "hello")
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client, err := NewClient(&config.Config{
		S3Endpoint: strings.TrimPrefix(server.URL, "http://"), S3Bucket: "bucket",
		S3Region: "eu-central-1", S3AccessKey: "test", S3SecretKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	// A non-seekable reader must continue to work with the new uploader.
	if err := client.Upload(ctx, key, io.LimitReader(strings.NewReader("hello"), 5), 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	object, err := client.GetObject(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(body) != "hello" || object.Info.Size != 5 || object.Info.ContentType != "text/plain" || object.Info.ETag != `"abc"` || !object.Info.Modified.Equal(modified) {
		t.Fatalf("unexpected object: %q, %+v, %v", body, object.Info, err)
	}
	if _, err := client.GetObject(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected storage.ErrNotFound, got %v", err)
	}
	if err := client.CopyObject(ctx, key, "copy"); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 7 {
		t.Fatalf("unexpected requests: %v", operations)
	}
}

func TestEnsureBucketDoesNotCreateOnAccessDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected bucket creation: %s", r.Method)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client, err := NewClient(&config.Config{S3Endpoint: strings.TrimPrefix(server.URL, "http://"), S3Bucket: "bucket"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureBucket(context.Background()); err == nil {
		t.Fatal("expected permission error")
	}
}

func TestEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"", "https://example.com", "example.com/path", "user:password@example.com", "example.com?query", "example.com#fragment"} {
		if _, err := NewClient(&config.Config{S3Endpoint: endpoint}); err == nil {
			t.Errorf("accepted invalid endpoint %q", endpoint)
		}
	}
}
