package site

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/storage/disk"
)

func TestExamsAssignmentChoicesAndSettingsDefaults(t *testing.T) {
	storage, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	b, db := identityBrowserWithStorage(t, storage)
	if _, err := db.Exec(`INSERT INTO programs(id,name) VALUES(1,'Informatik (B. Sc.)'),(2,'Wirtschaftsinformatik (B. Sc.)'),(3,'Informatik (M. Sc.)');
    INSERT INTO program_versions(program_id,name) VALUES(1,2016),(1,2023),(2,2023),(3,2016);
    INSERT INTO modules(id,program_id,name) VALUES(10,1,'Mathematik'),(20,2,'Mathematik'),(30,3,'Mathematik');
    INSERT INTO users(id,email,name,password,active,verified,program_id) VALUES(1,'preferences@council.example.invalid','Person','unused',1,1,1);
    INSERT INTO archive(id,group_id,user_id,module_id,version,exam_date,access_key,mime_type,nbytes,checksum) VALUES
    (100,100,1,10,2016,'2026-01-01','a','application/pdf',5,'one'),(200,200,1,20,2023,'2026-02-01','b','application/pdf',5,'two');`); err != nil {
		t.Fatal(err)
	}
	seedExamPublicIDs(t, db)
	if _, err := db.Exec(`INSERT INTO study_programs(id,name) VALUES(1,'Informatik'),(2,'Wirtschaftsinformatik');
		INSERT INTO program_qualifications(legacy_program_id,study_program_id,degree) VALUES(1,1,'Bachelor'),(2,2,'Bachelor'),(3,1,'Master');
		UPDATE programs SET name=CASE WHEN id=2 THEN 'Wirtschaftsinformatik' ELSE 'Informatik' END`); err != nil {
		t.Fatal(err)
	}
	program1 := publicRef(t, db, "study_programs", "1")
	program2 := publicRef(t, db, "study_programs", "2")
	document1 := "/exams/documents/" + examPublicID(t, db, "documents", "100")
	document2 := "/exams/documents/" + examPublicID(t, db, "documents", "200")
	if status, _, _ := b.get("/exams/settings"); status != 303 {
		t.Fatal("anonymous account preferences exposed")
	}
	session, err := users.NewService(db, "https://example.org", users.DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}}).CreateSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	if status, body, _ := b.get("/exams"); status != 200 || !strings.Contains(body, `href="`+document1+`"`) || strings.Contains(body, `href="`+document2+`"`) {
		t.Fatal("account qualification is not applied by default")
	}
	if status, body, _ := b.get("/exams?program=all"); status != 200 || !htmlContains(body, "Alle 2 Dokumente ansehen") || !strings.Contains(body, `href="`+document2+`"`) {
		t.Fatal("explicitly cleared filters still apply preferences")
	}
	if status, _, h := b.get("/exams?search=&program=&degree=&version="); status != 303 || h.Get("Location") != "/exams?program=all" {
		t.Fatal("empty form was not normalized to explicit all filters")
	}
	if status, body, _ := b.get("/exams/modules/" + examPublicID(t, db, "modules", "20") + "?program=all"); status != 200 || !strings.Contains(body, `href="`+document2+`"`) {
		t.Fatal("module navigation lost explicit clear")
	}
	if status, _, _ := b.post("/exams/settings", url.Values{}); status != 403 {
		t.Fatal("preferences accept missing CSRF")
	}
	if status, _, _ := b.post("/exams/settings", url.Values{"csrf": {session.CSRF}, "program": {program2}, "degree": {"bachelor"}, "version": {"2023"}}); status != 303 {
		t.Fatal("preferences could not be saved")
	}
	if status, body, _ := b.get("/exams"); status != 200 || !strings.Contains(body, `href="`+document2+`"`) || strings.Contains(body, `href="`+document1+`"`) {
		t.Fatal("stored preferences not applied")
	}
	if status, body, _ := b.get("/exams?program=" + program1 + "&degree=bachelor&version=2016"); status != 200 || !strings.Contains(body, `href="`+document1+`"`) {
		t.Fatal("explicit filter does not override preferences")
	}
	for _, path := range []string{"/exams?program=all", "/exams/settings", "/exams/submissions/create"} {
		status, body, _ := b.get(path)
		controls := strings.Join(strings.Fields(body), " ")
		if status != 200 || strings.Count(controls, `name="program" type="checkbox"`) != 2 || !strings.Contains(body, `value="`+program1+`"`) || !strings.Contains(body, `value="`+program2+`"`) {
			t.Fatalf("%s must show two canonical programme choices: %d", path, status)
		}
	}
	scope := url.Values{"program": {program1, program2}, "degree": {"bachelor", "master"}, "version": {"2016", "2023"}}
	if status, body, _ := b.get("/exams/submissions/create?" + scope.Encode()); status != 200 || !htmlContains(body, `name="module" type="checkbox" value="Mathematik"`) || strings.Contains(body, `name="assignment"`) {
		t.Fatal("upload does not offer a single module choice")
	}
	scope.Set("module_selection", "1")
	scope.Set("csrf", session.CSRF)
	if status, body, _ := b.post("/exams/submissions/create", scope); status != 422 || !strings.Contains(body, "Wähle mindestens ein passendes Modul aus.") {
		t.Fatal("empty module selection accepted")
	}
	scope.Set("module", "Unknown module")
	if status, body, _ := b.post("/exams/submissions/create", scope); status != 422 || !strings.Contains(body, "Wähle gültige Module aus.") {
		t.Fatal("unknown module accepted")
	}
	scope.Set("module", "Mathematik")
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	scope.Set("csrf", session.CSRF)
	scope.Set("date-day", "27")
	scope.Set("date-month", "3")
	scope.Set("date-year", "2026")
	for key, values := range scope {
		for _, value := range values {
			if err := form.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	file, err := form.CreateFormFile("files", "one.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "%PDF-1.7\none shared PDF\n%%EOF"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	status, _, headers := b.request("POST", "/exams/submissions/create", form.FormDataContentType(), &buffer)
	if status != 303 {
		t.Fatal("multiple assignments could not be submitted")
	}
	review := headers.Get("Location")
	requestID := strings.TrimSuffix(strings.TrimPrefix(review, "/exams/submissions/"), "/check")
	rows, err := db.Query(`SELECT p.module_id,p.version FROM archive a JOIN archive_placements p ON p.archive_id=a.id WHERE a.request_id=? ORDER BY p.module_id,p.version`, examInternalID(t, db, "submissions", requestID))
	if err != nil {
		t.Fatal(err)
	}
	var placements []string
	for rows.Next() {
		var module, version string
		if err := rows.Scan(&module, &version); err != nil {
			t.Fatal(err)
		}
		placements = append(placements, module+"|"+version)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if strings.Join(placements, ",") != "10|2016,10|2023,20|2023,30|2016" {
		t.Fatalf("module scope produced invalid assignments: %v", placements)
	}
	if status, body, _ := b.get(review); status != 200 || !strings.Contains(body, "Wirtschaftsinformatik") || !strings.Contains(body, "Master") || !strings.Contains(body, "Prüfungsordnungen") || strings.Contains(body, "Reko 2") {
		t.Fatalf("check answers does not show the actual placements for one PDF: %d %s", status, body)
	}
	edit := strings.TrimSuffix(review, "/check") + "/edit"
	if status, body, _ := b.get(edit); status != 200 || !htmlContains(body, `name="module" type="checkbox" value="Mathematik" checked`) || strings.Contains(body, `name="assignment"`) {
		t.Fatal("editing lost module selection or reintroduced tuple choices")
	}
}
