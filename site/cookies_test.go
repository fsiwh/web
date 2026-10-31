package site

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestCookieNoticeDoesNotCreateSession(t *testing.T) {
	c, _ := testApplication(t)
	for _, path := range []string{"/", "/cookies"} {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, w.Code)
		}
		if cookies := w.Result().Cookies(); len(cookies) != 0 {
			t.Fatalf("GET %s set cookies before a requested action: %v", path, cookies)
		}
		body := w.Body.String()
		if got, want := strings.Contains(body, "data-cookie-banner"), path != "/cookies"; got != want {
			t.Fatalf("GET %s: cookie banner present = %t, want %t", path, got, want)
		}
		if !strings.Contains(body, `href="/cookies"`) {
			t.Fatalf("GET %s: missing cookie details link", path)
		}
		if strings.Contains(body, "data-cookie-choice") {
			t.Fatalf("GET %s: unnecessary consent controls", path)
		}
		if path == "/cookies" && (!strings.Contains(body, "web-session") || !strings.Contains(body, "cookie-notice")) {
			t.Fatal("cookie details must describe both cookies")
		}
	}
}

func TestCookieNoticeInitialVisibility(t *testing.T) {
	c, _ := testApplication(t)
	for _, value := range []string{"", "dismissed-v1", "old-value"} {
		r := httptest.NewRequest(http.MethodGet, "/team", nil)
		if value != "" {
			r.AddCookie(&http.Cookie{Name: "cookie-notice", Value: value})
		}
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		banner := regexp.MustCompile(`<div[^>]*data-cookie-banner[^>]*>`).FindString(w.Body.String())
		if w.Code != http.StatusOK {
			t.Fatalf("page response: %d", w.Code)
		}
		if present := banner != ""; present != (value != "dismissed-v1") {
			t.Fatalf("cookie %q: initial banner present = %t", value, present)
		}
		if strings.Contains(banner, "hidden") {
			t.Fatal("an undismissed notice must be visible in the initial HTML")
		}
		if !strings.Contains(w.Header().Get("Vary"), "Cookie") {
			t.Fatal("cookie-dependent document must vary by Cookie")
		}
	}
}
