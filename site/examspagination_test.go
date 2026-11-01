package site

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/storage/disk"
)

func TestExamsPaginationKeepsFiltersAndGroupsModules(t *testing.T) {
	storage, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	b, db := identityBrowserWithStorage(t, storage)
	if _, err := db.Exec(`INSERT INTO programs(id,name) VALUES(1,'Informatik (B. Sc.)'); INSERT INTO program_versions(program_id,name) VALUES(1,2023); INSERT INTO users(id,email,name,password) VALUES(1,'pagination@council.example.invalid','Person','unused')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 45; i++ {
		if _, err := db.Exec(`INSERT INTO modules(id,program_id,name) VALUES(?,1,?)`, i, fmt.Sprintf("Modul %02d", i)); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 2; j++ {
			id := i*10 + j
			if _, err := db.Exec(`INSERT INTO archive(id,group_id,user_id,module_id,version,exam_date,access_key,mime_type,nbytes,checksum) VALUES(?,?,1,?,2023,'2026-01-01',?,'application/pdf',5,?)`, id, id, i, fmt.Sprint(id), fmt.Sprint(id)); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedExamPublicIDs(t, db)
	if _, err := db.Exec(`UPDATE users SET active=1,verified=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	session, err := users.NewService(db, "http://example.org", users.DomainPolicy{}).CreateSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(b.server.URL)
	b.client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	programID := examPublicID(t, db, "programs", "1")
	query := "?degree=bachelor&program=" + programID + "&version=2023&search=Modul"
	for _, tc := range []struct {
		page, rows      int
		present, absent string
	}{{1, 20, "Modul 45", "Modul 25"}, {2, 20, "Modul 25", "Modul 45"}, {3, 5, "Modul 01", "Modul 25"}} {
		path := "/exams" + query
		if tc.page > 1 {
			path += fmt.Sprintf("&page=%d", tc.page)
		}
		status, body, _ := b.get(path)
		if status != 200 || strings.Count(body, `scope="row"`) != tc.rows || !strings.Contains(body, tc.present) || strings.Contains(body, tc.absent) || !strings.Contains(body, "45 Module gefunden") || !strings.Contains(body, "Alle 2 Dokumente ansehen") {
			t.Fatalf("page %d: %d %s", tc.page, status, body)
		}
		if !strings.Contains(body, fmt.Sprintf("Lernmaterial (Seite %d von 3)", tc.page)) || !strings.Contains(body, `aria-current="page"`) {
			t.Fatal("missing page title or current state")
		}
		if !strings.Contains(body, `degree=bachelor&amp;program=`+programID+`&amp;search=Modul`) {
			t.Fatal("filter context lost")
		}
		if tc.page == 1 && strings.Contains(body, `rel="prev"`) || tc.page == 3 && strings.Contains(body, `rel="next"`) {
			t.Fatal("invalid boundary navigation")
		}
	}
	r, err := http.NewRequest("GET", b.server.URL+"/exams"+query+"&page=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("HX-Request", "true")
	response, err := b.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fragment), "<!doctype") || !strings.Contains(string(fragment), "Modul 25") || !strings.Contains(string(fragment), "data-document-title") {
		t.Fatal("fragment lost pagination or title")
	}
	for _, page := range []string{"0", "-1", "999", "invalid"} {
		status, _, _ := b.get("/exams" + query + "&page=" + page)
		want := http.StatusBadRequest
		if page == "999" {
			want = http.StatusNotFound
		}
		if status != want {
			t.Fatalf("invalid page %s: got %d want %d", page, status, want)
		}
	}
	if status, _, _ := b.get("/exams/modules/" + examPublicID(t, db, "modules", "1") + "?page=2"); status != http.StatusNotFound {
		t.Fatalf("out-of-range module page: %d", status)
	}
	status, body, _ := b.get("/exams?search=Modul+01")
	if status != 200 || !strings.Contains(body, "1 Modul gefunden") || strings.Contains(body, `class="govuk-pagination"`) {
		t.Fatal("single-result search has pagination")
	}
}
