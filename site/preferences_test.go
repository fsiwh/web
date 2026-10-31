package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAccountPreferencesRoutesAndOwnership(t *testing.T) {
	c, db := testApplication(t)
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("current password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,name,email,password,role,active,verified) VALUES(9042,'My Name','me@studmail.w-hs.de',?,'user',1,1),(9043,'Other Name','other@studmail.w-hs.de',?,'admin',1,1)`, string(hash), string(hash)); err != nil {
		t.Fatal(err)
	}
	s, err := c.identity.CreateSession(ctx, 9042)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, form url.Values, signedIn bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if signedIn {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ID})
		}
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/account", "/account/name", "/account/email", "/account/password", "/account/studies"} {
		if w := request("GET", path, nil, false); w.Code != 303 || w.Header().Get("Location") != "/login" {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
		w := request("GET", path, nil, true)
		if headings := strings.Count(w.Body.String(), "<h1"); headings != 1 {
			t.Fatalf("%s has %d page headings, want one", path, headings)
		}
		if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || strings.Contains(w.Body.String(), "Other Name") {
			t.Fatalf("authenticated %s: %d", path, w.Code)
		}
		if path != "/account" {
			body := w.Body.String()
			back := strings.Index(body, `class="govuk-back-link"`)
			main := strings.Index(body, "<main")
			if back < 0 || main < 0 || back > main || strings.Contains(body, `class="govuk-breadcrumbs"`) {
				t.Fatalf("%s must have a back link above main instead of breadcrumbs", path)
			}
			if w := request("POST", path, url.Values{"name": {"Changed Name"}}, true); w.Code != 403 {
				t.Fatalf("missing CSRF accepted at %s: %d", path, w.Code)
			}
		}
	}
	if w := request("POST", "/account/name", url.Values{"csrf": {s.CSRF}, "name": {"New Name"}, "id": {"9043"}, "role": {"admin"}}, true); w.Code != 303 {
		t.Fatalf("name update: %d %s", w.Code, w.Body.String())
	}
	var mine, other, role string
	if err := db.QueryRow(`SELECT name,role FROM users WHERE id=9042`).Scan(&mine, &role); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT name FROM users WHERE id=9043`).Scan(&other); err != nil || mine != "New Name" || other != "Other Name" || role != "user" {
		t.Fatal("form changed another account or role", mine, other, role, err)
	}
	for _, path := range []string{"/account/email", "/account/password"} {
		w := request("POST", path, url.Values{"csrf": {s.CSRF}, "email": {"new@studmail.w-hs.de"}, "current-password": {"wrong-secret-value"}, "password": {"new-secret-value"}}, true)
		body := w.Body.String()
		if w.Code != 422 || !strings.Contains(body, "govuk-error-summary") || !strings.Contains(body, `href="#current-password"`) || !strings.Contains(body, "Fehler:") {
			t.Fatalf("invalid current password %s: %d %s", path, w.Code, body)
		}
		if strings.Contains(body, "wrong-secret-value") || strings.Contains(body, "new-secret-value") {
			t.Fatal("validation response reflected a password")
		}
	}
	w := request("POST", "/account/email", url.Values{"csrf": {s.CSRF}, "email": {"new@studmail.w-hs.de"}, "current-password": {"current password"}}, true)
	if w.Code != 303 {
		t.Fatalf("email request: %d %s", w.Code, w.Body.String())
	}
	var link string
	if err := db.QueryRow(`SELECT link FROM mail_outbox WHERE user_id=9042 AND kind='verify'`).Scan(&link); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if w := request("GET", u.RequestURI(), nil, true); w.Code != 200 {
		t.Fatal("confirmation GET failed", w.Code)
	}
	p, err := c.identity.Preferences(ctx, 9042)
	if err != nil || p.Email != "me@studmail.w-hs.de" || p.PendingEmail != "new@studmail.w-hs.de" {
		t.Fatal("GET confirmation consumed link or wrong pending email", p, err)
	}
	w = request("GET", "/account", nil, true)
	if !strings.Contains(w.Body.String(), "Bestätige deine neue E-Mail-Adresse") || !strings.Contains(w.Body.String(), "new@studmail.w-hs.de") {
		t.Fatal("missing pending email instructions")
	}
	w = request("POST", "/email/confirm", url.Values{"csrf": {s.CSRF}, "token": {u.Query().Get("token")}}, true)
	if w.Code != 303 {
		t.Fatalf("email confirmation: %d %s", w.Code, w.Body.String())
	}
	p, err = c.identity.Preferences(ctx, 9042)
	if err != nil || p.Email != "new@studmail.w-hs.de" || p.PendingEmail != "" {
		t.Fatal("confirmation did not update address", p, err)
	}
	w = request("POST", "/account/password", url.Values{"csrf": {s.CSRF}, "current-password": {"current password"}, "password": {"replacement password"}}, true)
	if w.Code != 303 {
		t.Fatalf("password update: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/account", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Dein Passwort wurde geändert") {
		t.Fatal("password change lost login or success feedback", w.Code)
	}
}
