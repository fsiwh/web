package site

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/storage/disk"
)

func seedExamPublicIDs(t *testing.T, db *sql.DB) {
	t.Helper()
	for namespace, table := range map[string]string{"modules": "modules", "programs": "programs", "documents": "archive", "submissions": "archive_requests", "users": "users"} {
		rows, err := db.Query("SELECT CAST(id AS TEXT) FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, id := range ids {
			if _, err := publicid.Ensure(context.Background(), db, namespace, id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func examPublicID(t *testing.T, db *sql.DB, namespace, id string) string {
	t.Helper()
	value, err := publicid.Lookup(context.Background(), db, namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func examInternalID(t *testing.T, db *sql.DB, namespace, id string) string {
	t.Helper()
	value, err := publicid.Resolve(context.Background(), db, namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestExamsFilterRetainsStableValuesAndExplicitAll(t *testing.T) {
	program := publicid.New()
	for _, value := range []string{program, "all"} {
		r := httptest.NewRequest("GET", "/exams?program="+value+"&degree=bachelor&version=2023", nil)
		f := examsFilter(r)
		if len(f.Programs) != 1 || f.Programs[0] != value || len(f.Degrees) != 1 || f.Degrees[0] != "bachelor" {
			t.Fatalf("query contract lost: %+v", f)
		}
	}
}

func TestExamsDownloadRequiresLiveAuthenticatedSession(t *testing.T) {
	storage, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	b, db := identityBrowserWithStorage(t, storage)
	_, err = db.Exec(`
INSERT INTO programs (id,name) VALUES (1,'Informatik (B. Sc.)');
INSERT INTO program_versions (program_id,name) VALUES (1,'2023');
INSERT INTO modules (id,program_id,name) VALUES (10,1,'Datenbanken');
INSERT INTO users (id,email,name,password,active,verified) VALUES (1,'reader@council.example.invalid','Person','unused',1,1);
INSERT INTO archive (id,group_id,user_id,module_id,version,exam_date,access_key,mime_type,nbytes,checksum) VALUES (100,100,1,10,'2023','2026-01-01','key','application/pdf',9,'checksum');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Upload(context.Background(), "archive/100/source.pdf", strings.NewReader("%PDF-test"), 9, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	seedExamPublicIDs(t, db)
	documentPath := "/exams/documents/" + examPublicID(t, db, "documents", "100")
	protectedPaths := []string{"/exams", "/exams/modules/" + examPublicID(t, db, "modules", "10"), documentPath}
	if status, body, _ := b.get("/"); status != 200 || !strings.Contains(strings.SplitN(body, "</nav>", 2)[0], `href="/exams"`) {
		t.Fatal("anonymous navigation is missing learning material tab")
	}
	for _, path := range protectedPaths {
		if status, _, headers := b.get(path); status != 303 || headers.Get("Location") != "/login" || headers.Get("Cache-Control") != "private, no-store" {
			t.Fatal("anonymous PDF download allowed or cacheable")
		}
	}
	session, err := users.NewService(db, "http://example.org", users.DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}}).CreateSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(b.server.URL)
	b.client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	request, _ := http.NewRequest("GET", b.server.URL+documentPath, nil)
	request.Header.Set("Range", "bytes=0-4")
	response, err := b.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	value, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 206 || string(value) != "%PDF-" || response.Header.Get("Content-Type") != "application/pdf" {
		t.Fatal("authenticated PDF range download failed")
	}
	if status, body, _ := b.request("HEAD", documentPath, "", nil); status != 200 || body != "" {
		t.Fatal("PDF HEAD failed")
	}
	if status, body, _ := b.get("/exams"); status != 200 || !strings.Contains(body, `href="`+documentPath+`"`) || strings.Contains(body, "example.pdf") {
		t.Fatal("archive still uses example downloads")
	}
	for _, path := range protectedPaths {
		if status, body, headers := b.get(path); status != 200 || headers.Get("Cache-Control") != "private, no-store" || path == "/exams" && !strings.Contains(strings.SplitN(body, "</nav>", 2)[0], `href="/exams"`) {
			t.Fatalf("verified access failed at %s: %d", path, status)
		}
	}
	if _, err := db.Exec(`UPDATE users SET verified=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for _, path := range protectedPaths {
		if status, _, headers := b.get(path); status != 303 || headers.Get("Location") != "/login" {
			t.Fatalf("unverified access allowed at %s: %d", path, status)
		}
	}
	if _, err := db.Exec(`UPDATE users SET active=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := b.get(documentPath); status != 303 {
		t.Fatal("deactivated user retained PDF access")
	}
}

func TestAuthenticatedSinglePDFSubmission(t *testing.T) {
	storage, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	b, db := identityBrowserWithStorage(t, storage)
	if _, err := db.Exec(`
INSERT INTO programs(id,name) VALUES (1,'Informatik (B. Sc.)');
INSERT INTO program_versions(program_id,name) VALUES (1,'2023');
INSERT INTO modules(id,program_id,name) VALUES (10,1,'Datenbanken');
INSERT INTO programs(id,name) VALUES (2,'Medieninformatik (B. Sc.)');
INSERT INTO program_versions(program_id,name) VALUES (2,'2025');
INSERT INTO modules(id,program_id,name) VALUES (20,2,'Fremdes Modul');
INSERT INTO users(id,email,name,password,active,verified) VALUES (1,'author@council.example.invalid','Person','unused',1,1);`); err != nil {
		t.Fatal(err)
	}
	seedExamPublicIDs(t, db)
	programID := examPublicID(t, db, "programs", "1")
	moduleID := examPublicID(t, db, "modules", "10")
	if status, _, _ := b.get("/exams/submissions/create"); status != 303 {
		t.Fatal("anonymous upload form allowed")
	}
	session, err := users.NewService(db, "http://example.org", users.DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}}).CreateSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(b.server.URL)
	b.client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	if status, body, _ := b.get("/exams/submissions/create"); status != 200 || !strings.Contains(body, "Studiengänge") {
		t.Fatal("program selection is missing")
	}
	status, body, _ := b.get("/exams/submissions/create?program=" + programID + "&degree=bachelor&version=2023")
	if status != 200 {
		t.Fatal("upload form unavailable")
	}
	csrf := csrfFrom(t, body)
	if strings.Contains(body, "Fremdes Modul") {
		t.Fatal("upload catalog is not scoped to the selected program")
	}
	if status, body, _ := b.post("/exams/submissions/create", url.Values{"csrf": {csrf}, "program": {programID}, "degree": {"bachelor"}, "version": {"2023"}}); status != 422 || strings.Index(body, `href="#module_id"`) > strings.Index(body, `href="#date-day"`) {
		t.Fatal("upload errors do not follow field order")
	}
	var reviewPath string
	for _, value := range []string{"", csrf + "-multiple", csrf} {
		var buffer bytes.Buffer
		form := multipart.NewWriter(&buffer)
		for name, field := range map[string]string{"csrf": strings.TrimSuffix(value, "-multiple"), "assignment": moduleID + "|2023", "program": programID, "degree": "bachelor", "version": "2023", "date": "12.02.2026"} {
			if err := form.WriteField(name, field); err != nil {
				t.Fatal(err)
			}
		}
		filenames := []string{"eins.pdf"}
		if strings.HasSuffix(value, "-multiple") {
			filenames = append(filenames, "zwei.pdf")
		}
		for _, filename := range filenames {
			part, err := form.CreateFormFile("files", filename)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(part, "%PDF-1.7\n"+filename+"\n%%EOF"); err != nil {
				t.Fatal(err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		status, body, headers := b.request("POST", "/exams/submissions/create", form.FormDataContentType(), &buffer)
		if value == "" {
			if status != 403 {
				t.Fatal("upload accepted without CSRF")
			}
		} else if strings.HasSuffix(value, "-multiple") {
			if status != 422 || !strings.Contains(body, "Wähle genau eine Datei aus.") {
				t.Fatal("multiple PDF upload accepted")
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM archive").Scan(&count); err != nil || count != 0 {
				t.Fatal("rejected multi-file upload wrote records")
			}
		} else {
			reviewPath = headers.Get("Location")
			if status != 303 || !strings.HasPrefix(reviewPath, "/exams/submissions/") || !strings.HasSuffix(reviewPath, "/check") {
				t.Fatalf("single upload: %d %s", status, body)
			}
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM archive WHERE is_latest=1`).Scan(&count); err != nil || count != 0 {
		t.Fatal("draft PDFs were immediately published")
	}
	if status, body, _ := b.get(reviewPath); status != 200 || !strings.Contains(body, "Prüfe deine Angaben") || strings.Contains(body, "Reko 2") {
		t.Fatal("check-answers review missing")
	}
	// Enhanced uploads must redirect with a 200 response: browsers follow a 303
	// before HTMX can inspect its HX-Redirect header.
	var enhanced bytes.Buffer
	writer := multipart.NewWriter(&enhanced)
	for key, value := range map[string]string{"csrf": csrf, "assignment": moduleID + "|2023", "program": programID, "degree": "bachelor", "version": "2023", "date": "27.03.2026"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("files", "enhanced.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "%PDF-1.7\n%%EOF"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", b.server.URL+"/exams/submissions/create", &enhanced)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	response, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || !strings.HasSuffix(response.Header.Get("HX-Redirect"), "/check") {
		t.Fatal("enhanced upload does not redirect to the saved draft")
	}
	if status, _, _ := b.post(reviewPath, url.Values{"csrf": {csrf}}); status != 303 {
		t.Fatal("user confirmation failed")
	}
	requestID := strings.TrimSuffix(strings.TrimPrefix(reviewPath, "/exams/submissions/"), "/check")
	adminPath := "/admin/submissions/" + requestID
	if status, _, _ := b.get(adminPath); status != 403 {
		t.Fatal("ordinary user accessed administration review")
	}
	if status, _, _ := b.get(adminPath + "/edit"); status != 403 {
		t.Fatal("ordinary uploader accessed reviewed editing")
	}
	if _, err := db.Exec(`UPDATE users SET role='editor' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	session, err = users.NewService(db, "http://example.org", users.DomainPolicy{Accounts: []string{"students.example.invalid", "council.example.invalid"}}).CreateSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	csrf = session.CSRF
	if status, body, _ := b.get("/admin/submissions"); status != 200 || !strings.Contains(body, requestID) {
		t.Fatal("editor cannot find submitted request")
	}
	if status, _, _ := b.get("/admin/users"); status != 403 {
		t.Fatal("review permission granted user administration")
	}
	if status, _, _ := b.post(adminPath+"/review", url.Values{"decision": {"approve"}}); status != 403 {
		t.Fatal("approval accepted without CSRF")
	}
	reviewToken := func() string {
		t.Helper()
		status, body, _ := b.get(adminPath)
		_, rest, ok := strings.Cut(body, `name="edit_token" value="`)
		token, _, _ := strings.Cut(rest, `"`)
		if status != 200 || !ok || token == "" {
			t.Fatal("review page missing concurrency token")
		}
		return token
	}
	initialToken := reviewToken()
	for _, path := range []string{adminPath + "/review", adminPath + "/review?edit_token=" + initialToken} {
		if status, _, _ := b.post(path, url.Values{"csrf": {csrf}, "decision": {"approve"}}); status != 409 {
			t.Fatal("review accepted without a POST-body concurrency token")
		}
	}
	if status, _, _ := b.post(adminPath+"/review", url.Values{"csrf": {csrf}, "decision": {"approve"}, "edit_token": {initialToken}}); status != 303 {
		t.Fatal("editor approval failed")
	}
	if status, _, _ := b.post(adminPath+"/review", url.Values{"csrf": {csrf}, "decision": {"reject"}, "edit_token": {initialToken}}); status != 409 {
		t.Fatal("stale review reversed an intervening approval")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM archive WHERE is_latest=1`).Scan(&count); err != nil || count != 1 {
		t.Fatal("confirmed PDFs were not published")
	}
	if status, body, _ := b.get("/exams/modules/" + moduleID); status != 200 || !htmlContains(body, "1 Dokument") || !htmlContains(body, "Neue Revision einreichen") {
		t.Fatal("uploaded PDFs not available in module view")
	}
	for _, decision := range []string{"reject", "approve"} {
		if status, body, _ := b.get(adminPath); status != 200 || !htmlContains(body, `name="decision" value="`+decision+`"`) {
			t.Fatal("completed review does not offer the opposite decision")
		}
		if status, _, _ := b.post(adminPath+"/review", url.Values{"decision": {decision}}); status != 403 {
			t.Fatal("decision reversal accepted without CSRF")
		}
		if status, _, _ := b.post(adminPath+"/review", url.Values{"csrf": {csrf}, "decision": {decision}, "edit_token": {reviewToken()}}); status != 303 {
			t.Fatal("decision could not be reversed")
		}
		want := 0
		if decision == "approve" {
			want = 1
		}
		if err := db.QueryRow(`SELECT count(*) FROM archive WHERE is_latest=1`).Scan(&count); err != nil || count != want {
			t.Fatal("reversed decision did not update public archive")
		}
	}
	status, body, _ = b.get(adminPath + "/edit")
	if status != 200 {
		t.Fatal("reviewer cannot edit approved submission")
	}
	_, tokenPart, ok := strings.Cut(body, `name="edit_token" value="`)
	if !ok {
		t.Fatal("missing edit concurrency token")
	}
	token, _, _ := strings.Cut(tokenPart, `"`)
	edit := url.Values{"module_selection": {"1"}, "module": {"Datenbanken"}, "degree": {"bachelor"}, "program": {programID}, "version": {"2023"}, "date-day": {"27"}, "date-month": {"3"}, "date-year": {"2026"}, "comment": {"Redaktionell korrigiert"}, "edit_token": {token}}
	if status, _, _ := b.post(adminPath+"/edit", edit); status != 403 {
		t.Fatal("reviewer edit accepted without CSRF")
	}
	edit.Set("csrf", csrf)
	if status, _, headers := b.post(adminPath+"/edit", edit); status != 303 || headers.Get("Location") != adminPath {
		t.Fatal("reviewer edit did not return to review")
	}
	if status, body, _ := b.get(adminPath); status != 200 || !strings.Contains(body, "Redaktionell korrigiert") || !strings.Contains(body, adminPath+"/edit#comment") || strings.Contains(body, "govuk-button--warning") {
		t.Fatal("reviewed corrections or neutral actions missing")
	}
	if status, _, _ := b.post(adminPath+"/edit", edit); status != 409 {
		t.Fatal("stale reviewer edit accepted")
	}
	if status, _, _ := b.post(adminPath+"/review", url.Values{"csrf": {csrf}, "decision": {"reject"}, "edit_token": {token}}); status != 409 {
		t.Fatal("stale review accepted after content correction")
	}
	var documentID string
	if err := db.QueryRow(`SELECT id FROM archive WHERE request_id=? AND is_latest=1 LIMIT 1`, examInternalID(t, db, "submissions", requestID)).Scan(&documentID); err != nil {
		t.Fatal(err)
	}
	documentID = examPublicID(t, db, "documents", documentID)
	if code, body, _ := b.get("/exams/submissions/create?revision=" + documentID); code != 200 || !strings.Contains(body, `name="revision" value="`+documentID+`"`) {
		t.Fatal("revision creation form lost stable document ID")
	}
	var revised bytes.Buffer
	revisionForm := multipart.NewWriter(&revised)
	for key, value := range map[string]string{"csrf": csrf, "revision": documentID, "program": programID, "degree": "bachelor", "version": "2023", "module": "Datenbanken", "module_selection": "1", "date-day": "27", "date-month": "3", "date-year": "2026"} {
		if err := revisionForm.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := revisionForm.CreateFormFile("files", "revision.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "%PDF-1.7\nrevised content\n%%EOF"); err != nil {
		t.Fatal(err)
	}
	if err := revisionForm.Close(); err != nil {
		t.Fatal(err)
	}
	code, body, h := b.request("POST", "/exams/submissions/create", revisionForm.FormDataContentType(), &revised)
	if code != 303 {
		t.Fatalf("revision draft: %d %s", code, body)
	}
	revisionID := strings.TrimSuffix(strings.TrimPrefix(h.Get("Location"), "/exams/submissions/"), "/check")
	var revisionOf string
	if err := db.QueryRow(`SELECT revision_of FROM archive_requests WHERE id=?`, examInternalID(t, db, "submissions", revisionID)).Scan(&revisionOf); err != nil || revisionOf != examInternalID(t, db, "documents", documentID) {
		t.Fatalf("revision POST lost document relationship: %q %v", revisionOf, err)
	}
}
