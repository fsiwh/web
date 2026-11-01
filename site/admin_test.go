package site

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/model/users"
)

type adminBrowser struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
	csrf   string
}

func newAdminBrowser(t *testing.T, server *httptest.Server) *adminBrowser {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &adminBrowser{t: t, server: server, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (b *adminBrowser) request(method, path, contentType string, body io.Reader) (int, string, http.Header) {
	b.t.Helper()
	r, err := http.NewRequest(method, b.server.URL+path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	response, err := b.client.Do(r)
	if err != nil {
		b.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return response.StatusCode, string(data), response.Header
}
func (b *adminBrowser) get(path string) (int, string, http.Header) {
	return b.request("GET", path, "", nil)
}
func (b *adminBrowser) post(path string, form url.Values) (int, string, http.Header) {
	return b.request("POST", path, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
}
func csrfFrom(t *testing.T, body string) string {
	t.Helper()
	match := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(body)
	if match == nil {
		t.Fatal("missing CSRF token")
	}
	return match[1]
}
func (b *adminBrowser) login() {
	b.loginAs("admin@council.example.invalid")
}

func TestAdminAuthenticationAndMutations(t *testing.T) {
	handler, db := testApplication(t)
	seedPublicContent(t, db)
	member := publicRef(t, db, "users", "800")
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	for _, path := range []string{"/admin", "/admin/users", "/admin/users/" + member + "/edit", "/admin/meetings/create"} {
		if status, _, headers := b.get(path); status != 303 || headers.Get("Location") != "/login" {
			t.Fatalf("unprotected route: %s", path)
		}
	}
	_, body, _ := b.get("/login")
	token := csrfFrom(t, body)
	if status, _, _ := b.post("/login", url.Values{"email": {"admin@council.example.invalid"}, "password": {"wrong"}, "csrf": {token}}); status != 422 {
		t.Fatal("wrong credentials accepted")
	}
	if status, _, _ := b.post("/login", url.Values{"email": {"admin@council.example.invalid"}, "password": {"admin@council.example.invalid"}}); status != 403 {
		t.Fatal("login CSRF accepted")
	}
	b.login()
	for _, alias := range []string{"/admin/users/42", "/admin/members/1/edit", "/admin/roles/1/edit"} {
		if status, _, headers := b.get(alias); status != 404 || headers.Get("Location") != "" {
			t.Fatalf("numeric admin alias accepted: %s (%d)", alias, status)
		}
	}
	if status, body, headers := b.get("/admin/users?search=kim"); status != 200 || !strings.Contains(body, "Kim Muster") || strings.Contains(body, "Alex Beispiel") || headers.Get("Cache-Control") != "no-store" {
		t.Fatal("user search/cache control failed")
	}
	if status, _, _ := b.get("/admin/users/missing"); status != 404 {
		t.Fatal("unknown user accepted")
	}
	if status, _, _ := b.post("/admin/users/"+member+"/edit", url.Values{"name": {"Changed"}}); status != 403 {
		t.Fatal("write without CSRF accepted")
	}
	if status, body, _ := b.post("/admin/users/"+member+"/edit", url.Values{"csrf": {b.csrf}, "name": {""}, "public_email": {"not-email"}}); status != 422 || !strings.Contains(body, "govuk-error-summary") {
		t.Fatal("invalid member accepted")
	}
	if status, _, _ := b.post("/admin/users/"+member+"/edit", url.Values{"csrf": {b.csrf}, "name": {"Demo-Vorsitz"}, "public_email": {"demo@students.example.invalid"}}); status != 303 {
		t.Fatal("member edit failed")
	}
	if _, body, _ := b.get("/team"); !strings.Contains(body, "Demo-Vorsitz") {
		t.Fatal("public team did not reflect edit")
	}
	if status, _, headers := b.post("/admin/roles/create", url.Values{"csrf": {b.csrf}, "title": {"Demo-Referat"}, "group": {"departments"}, "members": {member}}); status != 303 || headers.Get("Location") != "/admin/roles" {
		t.Fatal("responsibility add failed")
	}
	if _, body, _ := b.get("/team"); !strings.Contains(body, "Demo-Referat") {
		t.Fatal("public responsibility missing")
	}
	if status, body, _ := b.get("/admin/users/" + member); status != 200 || !strings.Contains(body, "Demo-Vorsitz") || !strings.Contains(body, "Demo-Referat") {
		t.Fatal("member overview lost assignments")
	}
	if status, body, _ := b.get("/admin/roles"); status != 200 || !strings.Contains(body, "Demo-Referat") || !strings.Contains(body, "Demo-Vorsitz") {
		t.Fatal("function overview lost member assignments")
	}
	oldCookies := b.client.Jar.Cookies(mustURL(t, server.URL))
	if status, _, _ := b.post("/logout", url.Values{"csrf": {b.csrf}}); status != 303 {
		t.Fatal("logout failed")
	}
	b.client.Jar.SetCookies(mustURL(t, server.URL), oldCookies)
	if status, _, _ := b.get("/admin"); status != 303 {
		t.Fatal("logged-out session replayed")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (b *adminBrowser) upload(path string, fields map[string]string, data []byte) (int, string, http.Header) {
	b.t.Helper()
	var buffer bytes.Buffer
	w := multipart.NewWriter(&buffer)
	for key, value := range fields {
		if err := w.WriteField(key, value); err != nil {
			b.t.Fatal(err)
		}
	}
	if data != nil {
		file, err := w.CreateFormFile("file", "beispiel.pdf")
		if err != nil {
			b.t.Fatal(err)
		}
		file.Write(data)
	}
	w.Close()
	return b.request("POST", path, w.FormDataContentType(), &buffer)
}

func TestPublicationReviewAndVisibility(t *testing.T) {
	handler, err := testHandler(t)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	other := newAdminBrowser(t, server)
	other.loginAs("other@council.example.invalid")
	pdf, err := os.ReadFile("testdata/example.pdf")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"csrf": b.csrf, "day": "28", "month": "10", "year": "2027", "number": "45", "kind": "Einladung"}
	if status, _, _ := b.upload("/admin/meetings/create", fields, []byte("not a PDF")); status != 422 {
		t.Fatal("invalid file accepted")
	}
	fields["day"] = "32"
	status, body, _ := b.upload("/admin/meetings/create", fields, pdf)
	if status != 422 || !strings.Contains(body, "beibehalten") {
		t.Fatal("file was not retained through validation")
	}
	match := regexp.MustCompile(`id="admin-form"\s+action="([^"]+)"`).FindStringSubmatch(body)
	if match == nil {
		t.Fatal("draft form missing")
	}
	editPath := match[1]
	fields["day"] = "28"
	status, _, headers := b.upload(editPath, fields, nil)
	if status != 303 {
		t.Fatalf("valid draft status %d", status)
	}
	review := headers.Get("Location")
	if status, body, _ := b.get(review); status != 200 || !strings.Contains(body, "Veröffentlichung prüfen") {
		t.Fatal("review unavailable")
	}
	document := "/documents/" + strings.TrimSuffix(strings.TrimPrefix(editPath, "/admin/meetings/"), "/edit")
	if status, _, _ := other.get(review); status != 404 {
		t.Fatal("another admin can access private draft")
	}
	if status, _, _ := other.get(document); status != 404 {
		t.Fatal("another admin can access draft PDF")
	}
	if status, _, _ := b.get(document); status != 200 {
		t.Fatal("owner cannot preview draft")
	}
	if _, body, _ := other.get("/meetings"); strings.Contains(body, document) {
		t.Fatal("draft published before confirmation")
	}
	if status, _, _ := b.post(review, url.Values{"csrf": {b.csrf}}); status != 303 {
		t.Fatal("publication failed")
	}
	if status, body, _ := other.get("/meetings?year=2027"); status != 200 || !strings.Contains(body, document) {
		t.Fatal("published document missing from public archive")
	}
	guest := newAdminBrowser(t, server)
	if status, body, headers := guest.get(document); status != 200 || body != string(pdf) || headers.Get("Content-Type") != "application/pdf" {
		t.Fatal("published PDF not publicly readable")
	}
	// Repeated publish is idempotent rather than adding another document.
	_, body, _ = guest.get("/meetings?year=2027")
	count := strings.Count(body, document)
	b.post(review, url.Values{"csrf": {b.csrf}})
	_, body, _ = guest.get("/meetings?year=2027")
	if count == 0 || strings.Count(body, document) != count {
		t.Fatal("duplicate publication")
	}
}

func TestPasswordResetConfirmationPreview(t *testing.T) {
	handler, db := testApplication(t, []users.User{{ID: 42, Name: "Kim Beispiel", Email: "Kim@STUDENTS.EXAMPLE.INVALID", Role: "editor", Active: true}}...)
	account := "/admin/users/" + publicRef(t, db, "users", "42")
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	path := account + "/reset"
	if status, _, _ := b.get(path); status != 303 {
		t.Fatal("unprotected password reset")
	}
	b.login()
	if status, body, _ := b.get(account); status != 200 || !strings.Contains(body, "kim@students.example.invalid") || strings.Contains(body, "Kim@STUDENTS") || !strings.Contains(body, `<dialog`) {
		t.Fatal("user email normalization/dialog failed")
	}
	if status, body, _ := b.get(path); status != 200 || !strings.Contains(body, "Link zum Zurücksetzen senden") {
		t.Fatal("no-JS confirmation missing")
	}
	if status, _, _ := b.post(path, url.Values{}); status != 403 {
		t.Fatal("reset without CSRF accepted")
	}
	status, body, _ := b.post(path, url.Values{"csrf": {b.csrf}})
	if status != 303 {
		t.Fatalf("reset was not queued: %d %s", status, body)
	}
	if status, _, _ := b.get("/admin/users/missing/reset"); status != 404 {
		t.Fatal("unknown reset user accepted")
	}
}

func TestUserPaginationKeepsSearchContext(t *testing.T) {
	var records []users.User
	for i := 1; i <= 45; i++ {
		records = append(records, users.User{ID: int64(i), Name: fmt.Sprintf("Beispiel %02d", i), Email: fmt.Sprintf("user%d@students.example.invalid", i), Verified: i%2 == 0, Active: true})
	}
	handler, _ := testApplication(t, records...)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	status, body, _ := b.get("/admin/users?search=Beispiel&page=2")
	if status != 200 || !strings.Contains(body, "Benutzerkonten – Seite 2 von 3") || !htmlContains(body, `aria-current="page">2`) || !strings.Contains(body, `page=3&amp;search=Beispiel`) || !strings.Contains(body, "Beispiel 21") || strings.Contains(body, "Beispiel 01</a>") {
		t.Fatal("numbered pagination lost page/search context")
	}
	for _, tc := range []struct {
		value  string
		status int
	}{{"0", 400}, {"999", 404}, {"invalid", 400}} {
		status, _, headers := b.get("/admin/users?search=Beispiel&page=" + tc.value)
		if status != tc.status || headers.Get("Location") != "" {
			t.Fatalf("invalid page %q: status %d, location %q", tc.value, status, headers.Get("Location"))
		}
	}
	_, body, _ = b.get("/admin/users?search=absent")
	if strings.Contains(body, `aria-label="Benutzerseiten"`) {
		t.Fatal("pagination shown for empty results")
	}
}
