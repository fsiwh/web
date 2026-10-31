package site

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/view"

	"github.com/fachschaftinformatik/web/model/media"
	"github.com/fachschaftinformatik/web/storage"
)

func TestImagesNavigation(t *testing.T) {
	handler, db := testApplication(t)
	seedPublicContent(t, db)
	album := publicRef(t, db, "albums", "140125680309370881")
	first := publicRef(t, db, "media", "140126343856652289")
	last := publicRef(t, db, "media", "140126279717355521")
	image := publicRef(t, db, "media", "140126291885031425")
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/media", 200, "LAN Party 2025"},
		{"/media/albums/" + album, 200, "image-" + image},
		{"/media/albums/" + album + "?page=2", 404, "Seite nicht gefunden"},
		{"/media/" + last, 200, "/media/albums/" + album + "#image-" + last},
		{"/media/albums/140125680309370881", 404, "Seite nicht gefunden"},
		{"/media/140126279717355521", 404, "Seite nicht gefunden"},
		{"/media/unknown", 404, "Seite nicht gefunden"},
		{"/media/albums/unknown", 404, "Seite nicht gefunden"},
		{"/media/albums/140117348945231873/140126279717355521", 404, "Seite nicht gefunden"},
		{"/media/albums/" + album + "?page=3", 404, "Seite nicht gefunden"},
		{"/media/albums/" + album + "?page=-1", 400, ""},
		{"/media/albums/" + album + "?page=bad", 400, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", tc.path, nil))
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.contains) {
				t.Fatalf("status %d, body %s", response.Code, response.Body.String())
			}
			if tc.status >= 400 && response.Header().Get("Location") != "" {
				t.Fatal("invalid media URL redirected")
			}
		})
	}
	for _, tc := range []struct{ path, absent string }{
		{"/media/" + first, "Vorheriges Bild"},
		{"/media/" + last, "Nächstes Bild"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", tc.path, nil))
		if response.Code != 200 || strings.Contains(response.Body.String(), tc.absent) {
			t.Fatalf("unexpected content for %s: %s", tc.path, response.Body.String())
		}
	}
}

func TestLegacyAlbumPathDoesNotRedirect(t *testing.T) {
	handler, err := testHandler(t)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/events/140125680309370881?page=2", nil))
	if response.Code != 404 || response.Header().Get("Location") != "" {
		t.Fatal("legacy album URL redirected")
	}
}

type mediaBlob struct {
	storage.Provider
	stream bool
	keys   []string
	etag   string
}

type seekablePhoto struct{ *strings.Reader }

func (seekablePhoto) Close() error { return nil }

func (s *mediaBlob) GetObject(ctx context.Context, key string) (*storage.Object, error) {
	s.keys = append(s.keys, key)
	if key != "media/101/p/400.jpg" && key != "media/101/source" && key != "events/10/cover/p/400.jpg" {
		return nil, errors.New("missing")
	}
	var reader io.ReadCloser = seekablePhoto{strings.NewReader("image bytes")}
	if s.stream {
		reader = io.NopCloser(strings.NewReader("image bytes"))
	}
	return &storage.Object{ReadCloser: reader, Info: storage.ObjectInfo{
		Size: 11, ContentType: "application/octet-stream", ETag: s.etag,
		Modified: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
	}}, nil
}

func TestMediaCacheRevalidation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			objects := &mediaBlob{stream: stream, etag: `"photo-v1"`}
			c, db := liveMediaHandler(t, objects)
			image := publicRef(t, db, "media", "101")
			album := publicRef(t, db, "albums", "10")
			router := http.NewServeMux()
			router.HandleFunc("GET /images/{id}/{size}", c.MediaFile)
			router.HandleFunc("GET /media/albums/{id}/cover/{size}", c.AlbumCover)
			for _, path := range []string{"/images/" + image + "/400", "/media/albums/" + album + "/cover/400"} {
				for _, tc := range []struct {
					header, value string
					status        int
				}{
					{"", "", http.StatusOK},
					{"If-None-Match", `"photo-v1"`, http.StatusNotModified},
					{"If-None-Match", `W/"photo-v1"`, http.StatusNotModified},
					{"If-Modified-Since", "Sat, 31 Oct 2026 12:00:00 GMT", http.StatusNotModified},
					{"If-None-Match", `"old-photo"`, http.StatusOK},
				} {
					w := httptest.NewRecorder()
					r := httptest.NewRequest("GET", path, nil)
					if tc.header != "" {
						r.Header.Set(tc.header, tc.value)
					}
					router.ServeHTTP(w, r)
					if w.Code != tc.status || w.Header().Get("ETag") != objects.etag || (tc.status == http.StatusOK && w.Header().Get("Last-Modified") == "") {
						t.Fatalf("%s %s: status %d, headers %v", path, tc.header, w.Code, w.Header())
					}
					if tc.status == http.StatusNotModified && w.Body.Len() != 0 {
						t.Fatal("revalidation retransmitted image bytes")
					}
				}
			}
			if _, err := db.Exec("UPDATE media SET deleted_at='2026-10-31T13:00:00Z' WHERE id=101"); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/images/"+image+"/400", nil)
			r.Header.Set("If-None-Match", objects.etag)
			router.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("deleted image remained cacheable: %d %v", w.Code, w.Header())
			}
		})
	}
}

func liveMediaHandler(t *testing.T, storage storage.Provider) (*Handler, *sql.DB) {
	t.Helper()
	_, db := testApplication(t)
	_, err := db.Exec(`
INSERT INTO events (id,title,created_at,deleted_at) VALUES (10,'Live Album','2026-01-01T00:00:00Z',NULL),(20,'Other Album','2025-01-01T00:00:00Z',NULL),(30,'Deleted Album','2027-01-01T00:00:00Z','2027-02-01T00:00:00Z');
INSERT INTO media(id,event_id,user_id,title,description,access_key,mime_type,nbytes,created_at,deleted_at) VALUES (101,10,900,'Live Title','Real alt','101','image/jpeg',11,'2026-01-01T00:00:00Z',NULL),(102,10,900,'Hidden','Hidden','102','image/jpeg',11,'2026-01-01T00:00:00Z','2026-02-01T00:00:00Z'),(103,30,900,'Hidden','Hidden','103','image/jpeg',11,'2026-01-01T00:00:00Z',NULL),(104,10,900,'Unsafe','Unsafe','104','image/svg+xml',11,'2026-01-01T00:00:00Z',NULL);
UPDATE events SET cover_path='events/' || id || '/cover/source' WHERE id IN (10,30);`)
	if err != nil {
		t.Fatal(err)
	}
	for id := 110; id < 180; id++ {
		if _, err := db.Exec(`INSERT INTO media(id,event_id,user_id,title,access_key,mime_type,nbytes,created_at,homepage_visible) VALUES (?,10,900,?,?,'image/jpeg',11,'2025-01-01T00:00:00Z',1)`, id, "Bild "+strconv.Itoa(id), strconv.Itoa(id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE media SET homepage_visible=1`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	views, err := view.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{views: views, media: media.NewService(db, storage)}, db
}

func TestLiveMediaPaginationAndViewer(t *testing.T) {
	c, db := liveMediaHandler(t, nil)
	album := publicRef(t, db, "albums", "10")
	image := publicRef(t, db, "media", "101")
	pageImage := publicRef(t, db, "media", "115")
	router := http.NewServeMux()
	router.HandleFunc("GET /media", c.Media)
	router.HandleFunc("GET /media/albums/{album}", c.Media)
	router.HandleFunc("GET /media/{image}", c.Media)
	for _, tc := range []struct {
		path string
		code int
		want string
	}{
		{"/media", 200, "Live Album"},
		{"/media/albums/" + album, 200, "/images/" + image + "/400"},
		{"/media/albums/" + album + "?page=3", 200, "Bild 110"},
		{"/media/albums/" + album + "?page=4", 404, "Seite nicht gefunden"},
		{"/media/" + pageImage, 200, "/media/albums/" + album + "?page=3#image-" + pageImage},
		{"/media/" + image, 200, `alt="Real alt"`},
		{"/media/999", 404, "Seite nicht gefunden"},
		{"/media/" + publicRef(t, db, "media", "102"), 404, "Seite nicht gefunden"},
		{"/media/albums/" + publicRef(t, db, "albums", "30"), 404, "Seite nicht gefunden"},
		{"/media/101", 404, "Seite nicht gefunden"},
		{"/media/albums/10", 404, "Seite nicht gefunden"},
	} {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest("GET", tc.path, nil))
		if r.Code != tc.code || !strings.Contains(r.Body.String(), tc.want) {
			t.Errorf("%s: status %d, body %s", tc.path, r.Code, r.Body.String())
		}
	}
}

func TestMediaHTTPRangesAndPathProtections(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			storage := &mediaBlob{stream: stream}
			c, db := liveMediaHandler(t, storage)
			image := publicRef(t, db, "media", "101")
			router := http.NewServeMux()
			router.HandleFunc("GET /images/{id}/{size}", c.MediaFile)
			for _, tc := range []struct {
				method, path, byteRange string
				code                    int
				body                    string
			}{
				{"GET", "/images/" + image + "/400", "", 200, "image bytes"},
				{"GET", "/images/" + image + "/original", "bytes=2-5", 206, "age "},
				{"HEAD", "/images/" + image + "/original", "", 200, ""},
				{"GET", "/images/" + image + "/400", "bytes=100-", 416, ""},
				{"GET", "/images/" + publicRef(t, db, "media", "102") + "/400", "", 404, ""},
				{"GET", "/images/" + publicRef(t, db, "media", "103") + "/original", "", 404, ""},
				{"GET", "/images/" + publicRef(t, db, "media", "104") + "/original", "", 404, ""},
				{"GET", "/images/101/400", "", 404, ""},
				{"GET", "/images/999/original", "", 404, ""},
				{"GET", "/images/avatars/source", "", 404, ""},
				{"GET", "/images/" + image + "/%2e%2e%2fsource", "", 404, ""},
				{"GET", "/images/%2e%2e%2favatars%2f1/source", "", 404, ""},
				{"GET", "/images/" + image + "/800", "", 503, ""},
			} {
				r := httptest.NewRecorder()
				req := httptest.NewRequest(tc.method, tc.path, nil)
				req.Header.Set("Range", tc.byteRange)
				router.ServeHTTP(r, req)
				if r.Code != tc.code || ((tc.code == 200 || tc.code == 206) && r.Body.String() != tc.body) {
					t.Errorf("%s %s range %s: status %d, body %q", tc.method, tc.path, tc.byteRange, r.Code, r.Body.String())
				}
				if tc.code == 200 || tc.code == 206 {
					if r.Header().Get("Content-Type") != "image/jpeg" || r.Header().Get("Cache-Control") != "private, max-age=300, must-revalidate" || r.Header().Get("Content-Length") == "" || r.Header().Get("Accept-Ranges") != "bytes" {
						t.Errorf("missing image headers: %v", r.Header())
					}
					length := "11"
					if tc.code == 206 {
						length = "4"
						if got := r.Header().Get("Content-Range"); got != "bytes 2-5/11" {
							t.Errorf("Content-Range: %s", got)
						}
					}
					if got := r.Header().Get("Content-Length"); got != length {
						t.Errorf("Content-Length: %s, want %s", got, length)
					}
				}
			}
			for _, key := range storage.keys {
				if !strings.HasPrefix(key, "media/101/") {
					t.Errorf("unauthorized storage access: %s", key)
				}
			}
		})
	}
}

func TestEventCoverHTTPRangesAndVisibility(t *testing.T) {
	for _, stream := range []bool{false, true} {
		storage := &mediaBlob{stream: stream}
		c, db := liveMediaHandler(t, storage)
		album := publicRef(t, db, "albums", "10")
		router := http.NewServeMux()
		router.HandleFunc("GET /media/albums/{id}/cover/{size}", c.AlbumCover)
		for _, tc := range []struct {
			method, path string
			code         int
		}{{"GET", "/media/albums/" + album + "/cover/400", 206}, {"HEAD", "/media/albums/" + album + "/cover/400", 206}, {"GET", "/media/albums/" + publicRef(t, db, "albums", "20") + "/cover/400", 404}, {"GET", "/media/albums/" + publicRef(t, db, "albums", "30") + "/cover/400", 404}, {"GET", "/media/albums/999/cover/400", 404}, {"GET", "/media/albums/" + album + "/cover/original", 404}, {"GET", "/media/albums/10/cover/400", 404}} {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("Range", "bytes=2-5")
			router.ServeHTTP(response, request)
			if response.Code != tc.code {
				t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
			}
			if tc.code == 206 && (response.Header().Get("Content-Type") != "image/jpeg" || response.Header().Get("Content-Range") != "bytes 2-5/11" || (tc.method == "GET" && response.Body.String() != "age ")) {
				t.Fatalf("cover bytes or headers: %v %q", response.Header(), response.Body.String())
			}
		}
		for _, key := range storage.keys {
			if key != "events/10/cover/p/400.jpg" {
				t.Fatalf("unexpected storage access: %s", key)
			}
		}
	}
}
