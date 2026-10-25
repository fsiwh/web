package site

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/model/users"
)

func routePublicID(t *testing.T, db *sql.DB, namespace, id string) string {
	t.Helper()
	value, err := publicid.Ensure(context.Background(), db, namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRetiredRoutesAreNotAuthenticatedOrRedirected(t *testing.T) {
	c, _ := testApplication(t)
	for _, path := range []string{
		"/archive", "/archive/new", "/archive/preferences", "/archive/requests/1/review", "/archive/files/1", "/api/v1/archive/1/file",
		"/fachschaft", "/fachschaft/sitzungen", "/impressum", "/datenschutz", "/barrierefreiheit", "/discussions", "/d/new", "/d/1",
		"/community/neuigkeiten", "/community/veranstaltungen", "/community/events", "/events/1/2", "/media/1/400", "/media/events/1/cover/400",
		"/verwaltung", "/verwaltung/mitglieder/1", "/admin/mitglieder", "/admin/funktionen/create", "/admin/antraege/1", "/admin/stammdaten",
		"/forgot", "/reset", "/verify", "/invite", "/assets/content/old.pdf",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
			t.Run(method+path, func(t *testing.T) {
				w := httptest.NewRecorder()
				c.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
					t.Fatalf("%d Location=%q", w.Code, w.Header().Get("Location"))
				}
			})
		}
	}
}

func TestConcreteRoutePolicies(t *testing.T) {
	c, db := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	for _, path := range []string{"/posts/create", "/posts/1/edit", "/events/create", "/exams/settings", "/exams/documents/1", "/exams/submissions", "/admin/settings/identity", "/admin/roles/create", "/admin/submissions/1/review"} {
		path = strings.ReplaceAll(path, "/1", "/AAAAAAAAAAA")
		if code, _, h := b.get(path); code != 303 || h.Get("Location") != "/login" {
			t.Fatalf("unprotected %s: %d", path, code)
		}
	}
	b.login()
	for _, path := range []string{"/logout", "/posts/create", "/events/create", "/exams/settings", "/exams/submissions/create", "/admin/settings/identity", "/admin/members/create", "/admin/roles/create", "/admin/meetings/create", "/admin/submissions/1/review", "/admin/users/900/confirm"} {
		path = strings.NewReplacer("/1", "/AAAAAAAAAAA", "/900", "/AAAAAAAAAAA").Replace(path)
		if code, _, _ := b.post(path, url.Values{}); code != 403 {
			t.Fatalf("missing CSRF accepted at %s: %d", path, code)
		}
	}
	if _, err := db.Exec("UPDATE users SET role='editor' WHERE id=900"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/users", "/admin/members/create"} {
		if code, _, _ := b.get(path); code != 403 {
			t.Fatalf("reviewer accessed %s: %d", path, code)
		}
	}
	for _, path := range []string{"/admin/settings", "/admin/settings/identity", "/admin/settings/legal", "/admin/members", "/admin/roles", "/admin/roles/create", "/admin/meetings", "/admin/meetings/create", "/admin/modules", "/admin/programs", "/admin/regulations", "/admin/audit"} {
		if code, _, _ := b.get(path); code != 200 {
			t.Fatalf("editor content access denied %s: %d", path, code)
		}
	}
	if code, _, _ := b.get("/admin/submissions"); code != 200 {
		t.Fatalf("review queue inaccessible: %d", code)
	}
}

func TestCanonicalMethodContract(t *testing.T) {
	c, _ := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/logout"}, {"GET", "/posts/1/vote"}, {"GET", "/posts/1/comments"}, {"GET", "/posts/1/comments/1/edit"}, {"GET", "/admin/members/1/invite"}, {"GET", "/admin/users/900/confirm"},
		{"POST", "/exams"}, {"POST", "/posts"}, {"POST", "/events"}, {"POST", "/admin/members/1"}, {"POST", "/admin/meetings/1"}, {"POST", "/admin/submissions/1"},
	} {
		code, _, h := b.request(tc.method, tc.path, "", nil)
		if code != 404 && code != 405 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, code)
		}
		if h.Get("Location") != "" {
			t.Fatalf("unsupported method redirected: %s", tc.path)
		}
	}
}

func TestAccountBodyPolicyEnforcedBeforeHandler(t *testing.T) {
	c, _ := testApplication(t)
	s, err := c.identity.CreateSession(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/password/forgot", strings.NewReader(url.Values{"csrf": {s.CSRF}, "email": {strings.Repeat("x", 65<<10)}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ID})
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized account form: %d", w.Code)
	}
}

func TestUnknownRoutesIgnoreInvalidSessionCookie(t *testing.T) {
	c, _ := testApplication(t, users.User{ID: 42, Name: "Test", Email: "test@studmail.w-hs.de", Active: true})
	r := httptest.NewRequest("GET", "/admin/legacy/42", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "invalid"})
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("unknown route: %d", w.Code)
	}
}

func TestEffectiveExamFiltersSurviveModulePagination(t *testing.T) {
	c, db := testApplication(t)
	seedPublicContent(t, db)
	if err := c.exams.SaveSettings(context.Background(), 900, exams.Filter{Programs: []string{publicRef(t, db, "programs", "1")}, Degrees: []string{"bachelor"}, Versions: []string{"2023"}}); err != nil {
		t.Fatal(err)
	}
	for i := 200; i < 225; i++ {
		if _, err := db.Exec(`INSERT INTO archive(id,group_id,user_id,module_id,version,exam_date,access_key,mime_type,nbytes,checksum) VALUES(?,?,900,11,'2023','2026-01-01',?,'application/pdf',5,?)`, i, i, fmt.Sprint(i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	seedPublicRefs(t, db)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	moduleURL := urls.ExamModule(routePublicID(t, db, "modules", "11"))
	if code, body, _ := b.get(moduleURL + "?program=all&q=ignored&applied=1"); code != 200 || !strings.Contains(body, "page=2&amp;program=all") || strings.Contains(body, "q=ignored") || strings.Contains(body, "applied=1") {
		t.Fatalf("explicit all-program pagination lost: %d", code)
	}
	if code, body, _ := b.get(moduleURL + "?page=2&program=all"); code != 200 || !strings.Contains(body, `href="/exams/documents/`) {
		t.Fatal("second page reapplied saved settings")
	}
	if code, body, _ := b.get("/exams?q=SQL&seite=2"); code != 200 || !strings.Contains(body, "2 Module gefunden") || strings.Contains(body, "q=SQL") || strings.Contains(body, "seite=") {
		t.Fatal("retired query names remain active")
	}
}

func TestEventPostEditorUsesLinkedEventID(t *testing.T) {
	c, db := testApplication(t)
	if _, err := db.Exec(`INSERT INTO discussion_posts(id,title,body,user_id,type) VALUES(50,'Thread for event','Discussion remains readable',900,'event'); INSERT INTO calendar_events(id,title,body,user_id,legacy_post_id) VALUES(50,'Unrelated calendar event','Other',900,NULL),(60,'Linked calendar event','Event',900,50)`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	postURL := urls.Post(routePublicID(t, db, "posts", "50"))
	eventURL := urls.EditEvent(routePublicID(t, db, "events", "60"))
	if code, _, h := b.get(postURL + "/edit"); code != 303 || h.Get("Location") != eventURL {
		t.Fatalf("editor delegated to wrong resource: %d %s", code, h.Get("Location"))
	}
	if code, body, h := b.get(postURL); code != 200 || h.Get("Location") != "" || !strings.Contains(body, "Discussion remains readable") {
		t.Fatal("event thread no longer has canonical post detail")
	}
}

func TestCanonicalRoutesAndPagination(t *testing.T) {
	c, _ := testApplication(t)
	for _, tc := range []struct {
		method, path string
		status       int
		location     string
	}{
		{"GET", "/about/", 308, "/about"},
		{"GET", "https://untrusted.example/about/", 308, "/about"},
		{"HEAD", "/posts/?page=1&search=Go", 308, "/posts?search=Go"},
		{"GET", "/posts?page=01", 308, "/posts"},
		{"GET", "/missing/", 404, ""},
		{"GET", "/posts/0001/", 404, ""},
		{"GET", "/posts/AAAAAAAAAAB/", 404, ""},
		{"GET", "/posts?page=", 400, ""},
		{"GET", "/posts?page=0", 400, ""},
		{"GET", "/posts?page=-1", 400, ""},
		{"GET", "/posts?page=1&page=2", 400, ""},
		{"GET", "/posts?page=wat", 400, ""},
		{"GET", "/posts?page=999999", 404, ""},
		{"GET", "/events?page=999999", 404, ""},
		{"GET", "/media?page=999999", 404, ""},
		{"POST", "/about/", 404, ""},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			c.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || w.Header().Get("Location") != tc.location {
				t.Fatalf("status=%d location=%q", w.Code, w.Header().Get("Location"))
			}
			if strings.Contains(w.Body.String(), `rel="canonical"`) {
				t.Fatal("non-success response has canonical metadata")
			}
		})
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "https://untrusted.example/about?tracking=1", nil)
	c.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `rel="canonical" href="/about"`) || strings.Contains(w.Body.String(), "untrusted.example") {
		t.Fatalf("public canonical metadata: status=%d", w.Code)
	}
}

func TestCanonicalMetadataAllowlist(t *testing.T) {
	for _, tc := range []struct{ pattern, target, want string }{
		{urls.PostsPath, "/posts?page=2&search=Go&tracking=x", "/posts?page=2&search=Go"},
		{urls.PostPattern, "/posts/m7Qp2Vx9aK4?page=2&search=Go", "/posts/m7Qp2Vx9aK4?page=2"},
		{urls.MeetingsPath, "/meetings?year=2029&tracking=x", "/meetings?year=2029"},
		{urls.ExamModulePattern, "/exams/modules/AAAAAAAAAAA?page=2&program=all", "/exams/modules/AAAAAAAAAAA?page=2&program=all"},
		{urls.LoginPath, "/login?token=secret", ""},
		{urls.EditPostPattern, "/posts/AAAAAAAAAAA/edit", ""},
		{urls.SubmissionPattern, "/exams/submissions/AAAAAAAAAAA", ""},
		{urls.AdminUserPattern, "/admin/users/AAAAAAAAAAA", ""},
	} {
		r := httptest.NewRequest("GET", tc.target, nil)
		r.Pattern = "GET " + tc.pattern
		if got := canonicalURL(r); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestMeetingYearCanonicalAndEmptyCatalogue(t *testing.T) {
	c, db := testApplication(t)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/meetings?year=2029", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `rel="canonical" href="/meetings?year=2029"`) {
		t.Fatal("historical meeting year lost its canonical address", w.Code)
	}
	if _, err := db.Exec(`DELETE FROM historical_meeting_documents`); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/meetings", nil))
	if w.Code != 200 {
		t.Fatal("empty meeting catalogue must remain usable", w.Code)
	}
}
