package site

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/view"
)

func TestBrowserAssetsAreVersionedAndSessionIndependent(t *testing.T) {
	c, _ := testApplication(t)
	for _, name := range []string{"site.js", "govuk.js", "site.css", "govuk.css", "fonts/light-94a07e06a1-v2.woff2"} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, view.AssetPath(name), nil)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "irrelevant-session"})
			w := httptest.NewRecorder()
			c.ServeHTTP(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || strings.Contains(w.Header().Get("Vary"), "Cookie") || len(w.Result().Cookies()) != 0 {
				t.Fatalf("asset response: %d %v", w.Code, w.Header())
			}
			etag := w.Header().Get("ETag")
			if etag == "" {
				t.Fatal("missing asset validator")
			}
			if name == "govuk.css" && !strings.Contains(w.Body.String(), view.AssetPath("fonts/")) {
				t.Fatal("font URLs must use the same bundle as their preload")
			}
			r = httptest.NewRequest(http.MethodGet, view.AssetPath(name), nil)
			r.Header.Set("If-None-Match", etag)
			w = httptest.NewRecorder()
			c.ServeHTTP(w, r)
			if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
				t.Fatalf("revalidation: %d, %d bytes", w.Code, w.Body.Len())
			}
		})
	}
	for _, path := range []string{"/assets/missing.css", "/assets/" + strings.Repeat("0", 64) + "/site.css"} {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("missing bundle asset: %d %v", w.Code, w.Header())
		}
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/site.css", nil))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, no-cache" || w.Header().Get("ETag") == "" {
		t.Fatal("legacy URLs must remain available with revalidation")
	}
}
