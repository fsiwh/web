package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/fachschaftinformatik/web/internal/config"
	"github.com/fachschaftinformatik/web/storage"
)

func securityClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(&config.Config{
		S3Endpoint: strings.TrimPrefix(strings.TrimPrefix(server.URL, "https://"), "http://"),
		S3Bucket:   "private-bucket", S3UseSSL: strings.HasPrefix(server.URL, "https://"),
		S3AccessKey: "test-access", S3SecretKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRejectAmbiguousKeysBeforeStorageAccess(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	client := securityClient(t, server)
	ctx := context.Background()
	for _, key := range []string{"", "../other-bucket/secret", "a/../../secret", "a/./secret", "/other-bucket/secret", "a//b", "a/", `a\..\secret`, "a\x00b", "a\r\nx-amz-acl: public-read", "https://attacker.invalid/secret", string([]byte{0xff}), strings.Repeat("ü", 513)} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			if _, err := client.GetObject(ctx, key); err == nil {
				t.Error("GET accepted ambiguous key")
			}
			if err := client.Upload(ctx, key, strings.NewReader("x"), 1, "text/plain"); err == nil {
				t.Error("upload accepted ambiguous key")
			}
			if err := client.Delete(ctx, key); err == nil {
				t.Error("DELETE accepted ambiguous key")
			}
			if err := client.CopyObject(ctx, key, "safe"); err == nil {
				t.Error("copy accepted ambiguous source")
			}
			if err := client.CopyObject(ctx, "safe", key); err == nil {
				t.Error("copy accepted ambiguous destination")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("invalid keys reached object storage")
	}
}

func TestSpecialKeysCannotInjectBucketQueryOrACL(t *testing.T) {
	for _, key := range []string{
		"folder/a b+ü.txt", "file?acl&versionId=secret#fragment",
		"%2e%2e/%2fother-bucket/key", "file%0d%0ax-amz-acl:public-read",
	} {
		t.Run(key, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/private-bucket/"+key || r.URL.Query().Get("acl") != "" || r.URL.Query().Has("acl") || r.URL.Query().Has("versionId") {
					t.Errorf("key changed request target: %s", r.URL)
				}
				for name := range r.Header {
					lower := strings.ToLower(name)
					if lower == "x-amz-acl" || strings.HasPrefix(lower, "x-amz-grant-") {
						t.Errorf("unexpected privilege-changing header: %s", name)
					}
				}
				if r.URL.Query().Has("X-Amz-Credential") || r.URL.Query().Has("X-Amz-Signature") || strings.Contains(r.URL.String(), "test-secret") {
					t.Error("credentials or presigned URL leaked into request URL")
				}
				if source := r.Header.Get("X-Amz-Copy-Source"); source != "" {
					decoded, err := url.PathUnescape(source)
					if err != nil || decoded != "private-bucket/"+key || strings.ContainsAny(source, "?#\r\n") {
						t.Errorf("copy-source injection: %q", source)
					}
					fmt.Fprint(w, `<CopyObjectResult><ETag>"abc"</ETag></CopyObjectResult>`)
				} else if r.Method == "GET" {
					fmt.Fprint(w, "x")
				} else {
					io.Copy(io.Discard, r.Body)
					w.Header().Set("ETag", `"abc"`)
				}
			}))
			defer server.Close()
			client := securityClient(t, server)
			ctx := context.Background()
			object, err := client.GetObject(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			object.Close()
			if err := client.Upload(ctx, key, strings.NewReader("x"), 1, "text/plain"); err != nil {
				t.Fatal(err)
			}
			if err := client.CopyObject(ctx, key, key); err != nil {
				t.Fatal(err)
			}
			if err := client.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUploadRejectsSizeMismatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.Copy(io.Discard, r.Body)
	}))
	defer server.Close()
	client := securityClient(t, server)
	for _, size := range []int64{-1, 0, 4, 6} {
		if err := client.Upload(context.Background(), "safe", strings.NewReader("hello"), size, "text/plain"); err == nil {
			t.Errorf("accepted 5-byte stream with declared size %d", size)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid small upload reached object storage")
	}
}

func TestSignedRequestsDoNotFollowRedirects(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
	}))
	defer destination.Close()
	for _, status := range []int{301, 302, 307, 308} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, destination.URL+"/stolen", status)
		}))
		client := securityClient(t, server)
		if object, err := client.GetObject(context.Background(), "secret"); err == nil {
			object.Close()
			t.Errorf("redirect %d accepted", status)
		}
		if err := client.Upload(context.Background(), "secret", strings.NewReader("private"), 7, "text/plain"); err == nil {
			t.Errorf("upload redirect %d accepted", status)
		}
		server.Close()
	}
	if leaked.Load() != 0 {
		t.Fatal("redirect leaked a signed request or private upload")
	}
}

func TestAccessDeniedAndEmbeddedCopyErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Copy-Source") == "" {
			w.WriteHeader(http.StatusForbidden)
		}
		// S3 can report CopyObject failure inside HTTP 200.
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
	}))
	defer server.Close()
	client := securityClient(t, server)
	ctx := context.Background()
	if _, err := client.GetObject(ctx, "secret"); err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("permission failure incorrectly treated as missing: %v", err)
	}
	if err := client.CopyObject(ctx, "secret", "copy"); err == nil {
		t.Fatal("HTTP 200 embedded copy error ignored")
	}
	if err := client.Delete(ctx, "secret"); err == nil {
		t.Fatal("delete permission failure ignored")
	}
	if err := client.Upload(ctx, "secret", strings.NewReader("x"), 1, "text/plain"); err == nil {
		t.Fatal("upload permission failure ignored")
	}
}

func TestTLSCertificateVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted TLS connection accepted")
	}))
	defer server.Close()
	client := securityClient(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := client.GetObject(ctx, "secret"); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
}

func TestBucketAndEndpointConfigCannotSelectOtherResources(t *testing.T) {
	for _, bucket := range []string{"", "../other", "bucket/key", "bucket?acl", "arn:aws:s3:us-east-1:123456789012:accesspoint/other", "https://attacker.invalid"} {
		if _, err := NewClient(&config.Config{S3Endpoint: "localhost:9000", S3Bucket: bucket}); err == nil {
			t.Errorf("accepted resource selector as bucket %q", bucket)
		}
	}
	if _, err := NewClient(&config.Config{S3Endpoint: "user:very-secret@example.com", S3Bucket: "bucket"}); err == nil || strings.Contains(err.Error(), "very-secret") {
		t.Fatalf("invalid endpoint accepted or secret disclosed: %v", err)
	}
}

func TestFailedMultipartUploadAbortsWithoutCommit(t *testing.T) {
	for _, mode := range []string{"success", "access-denied", "truncated", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			var aborted, completed atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "POST" && r.URL.Query().Has("uploads"):
					fmt.Fprint(w, `<InitiateMultipartUploadResult><Bucket>private-bucket</Bucket><Key>secret</Key><UploadId>test-upload</UploadId></InitiateMultipartUploadResult>`)
				case r.Method == "PUT":
					io.Copy(io.Discard, r.Body)
					if mode == "access-denied" {
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
					} else {
						w.Header().Set("ETag", `"part"`)
					}
				case r.Method == "DELETE" && r.URL.Query().Get("uploadId") == "test-upload":
					aborted.Add(1)
					w.WriteHeader(204)
				case r.Method == "POST" && r.URL.Query().Has("uploadId"):
					completed.Add(1)
					fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
				default:
					t.Errorf("unexpected multipart request: %s %s", r.Method, r.URL)
				}
			}))
			defer server.Close()
			client := securityClient(t, server)
			client.uploader = transfermanager.New(client.s3, func(o *transfermanager.Options) {
				o.MultipartUploadThreshold = 1
			})
			data := strings.Repeat("x", 9<<20)
			size := int64(len(data))
			if mode == "truncated" {
				size++
			} else if mode == "oversized" {
				size--
			}
			err := client.Upload(context.Background(), "secret", strings.NewReader(data), size, "application/octet-stream")
			if mode == "success" {
				if err != nil || completed.Load() != 1 || aborted.Load() != 0 {
					t.Fatalf("valid multipart upload failed: %v, abort=%d, complete=%d", err, aborted.Load(), completed.Load())
				}
				return
			}
			if err == nil {
				t.Fatal("multipart failure ignored")
			}
			if aborted.Load() != 1 || completed.Load() != 0 {
				t.Fatalf("failed multipart left active or committed: abort=%d, complete=%d", aborted.Load(), completed.Load())
			}
		})
	}
}

func TestCancelledAndHeaderInjectionUploadsDoNotReachStorage(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client := securityClient(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Upload(ctx, "secret", strings.NewReader("x"), 1, "text/plain"); err == nil {
		t.Fatal("cancelled upload succeeded")
	}
	if err := client.Upload(context.Background(), "secret", strings.NewReader("x"), 1, "text/plain\r\nx-amz-acl: public-read"); err == nil {
		t.Fatal("Content-Type header injection accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("cancelled or injected upload reached storage")
	}
}
