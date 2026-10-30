package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGalleryManagement(t *testing.T) {
	c, db := testApplication(t)
	if _, err := db.Exec(`INSERT INTO events(id,title,created_at) VALUES(10,'Gallery test','2026-01-01T00:00:00Z');
INSERT INTO media(id,event_id,user_id,title,access_key,mime_type,nbytes,created_at) VALUES
(101,10,900,'One','101','image/jpeg',5,'2026-01-01T00:00:00Z'),
(102,10,900,'Two','102','image/jpeg',5,'2026-01-01T00:00:00Z'),
(103,10,900,'Three','103','image/jpeg',5,'2026-01-01T00:00:00Z'),
(104,10,900,'Four','104','image/jpeg',5,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE media SET homepage_visible=1`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(902,'gallery-user@fsi-wh.de','Member','unused','user',1,1)`); err != nil {
		t.Fatal(err)
	}
	staff, err := c.identity.CreateSession(context.Background(), 900)
	if err != nil {
		t.Fatal(err)
	}
	member, err := c.identity.CreateSession(context.Background(), 902)
	if err != nil {
		t.Fatal(err)
	}
	var internal string
	if err := db.QueryRow(`SELECT CAST(id AS TEXT) FROM media WHERE deleted_at IS NULL ORDER BY id LIMIT 1`).Scan(&internal); err != nil {
		t.Fatal(err)
	}
	photo := publicRef(t, db, "media", internal)
	request := func(method, path string, s *session, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		if s != nil {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ID})
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/media/manage", nil, nil); w.Code != 303 {
		t.Fatalf("anonymous management: %d", w.Code)
	}
	if w := request("GET", "/media/manage", member, nil); w.Code != 403 {
		t.Fatalf("member management: %d", w.Code)
	}
	if w := request("GET", "/media/manage", staff, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Auf der Startseite anzeigen") {
		t.Fatalf("staff management: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `/images/`+photo+`/1200`) {
		t.Fatalf("selected homepage image missing: %d", w.Code)
	}
	path := "/media/" + photo + "/visibility"
	if w := request("POST", path, staff, url.Values{"page": {"1"}, "csrf": {"invalid"}}); w.Code != 403 {
		t.Fatalf("invalid CSRF: %d", w.Code)
	}
	if w := request("POST", path, member, url.Values{"page": {"1"}, "csrf": {member.CSRF}}); w.Code != 403 {
		t.Fatalf("member mutation: %d", w.Code)
	}
	if w := request("POST", path, staff, url.Values{"page": {"1"}, "csrf": {staff.CSRF}}); w.Code != 303 {
		t.Fatalf("save selection: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/media/"+photo, nil, nil); w.Code != 200 {
		t.Fatalf("photo disappeared after homepage exclusion: %d", w.Code)
	}
	if w := request("GET", "/", nil, nil); w.Code != 200 || strings.Contains(w.Body.String(), `/images/`+photo+`/1200`) {
		t.Fatalf("unselected homepage image still present: %d", w.Code)
	}
	var visible bool
	if err := db.QueryRow(`SELECT homepage_visible FROM media WHERE id=?`, internal).Scan(&visible); err != nil || visible {
		t.Fatalf("visibility: %v %v", visible, err)
	}
	if w := request("POST", path, staff, url.Values{"page": {"1"}, "csrf": {staff.CSRF}, "homepage_visible": {"1"}}); w.Code != 303 {
		t.Fatalf("restore: %d", w.Code)
	}
	if w := request("GET", "/media/"+photo, nil, nil); w.Code != 200 {
		t.Fatalf("restored viewer: %d", w.Code)
	}
}
