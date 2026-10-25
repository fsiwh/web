package site

import (
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAdminModuleListFiltersAndPagination(t *testing.T) {
	handler, db := testApplication(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	if status, _, headers := b.get("/admin/modules"); status != 303 || headers.Get("Location") != "/login" {
		t.Fatal("anonymous access to module management")
	}
	b.login()
	if status, body, _ := b.get("/admin/modules"); status != 200 || !strings.Contains(body, "Es sind noch keine Module vorhanden.") {
		t.Fatal("empty module list missing")
	}
	if _, err := db.Exec(`INSERT INTO programs(id,name) VALUES(1,'Informatik (B. Sc.)'),(2,'Informatik (M. Sc.)'),(3,'Alt');
		INSERT INTO study_programs(id,name) VALUES(1,'Informatik');
		INSERT INTO program_qualifications(legacy_program_id,study_program_id,degree) VALUES(1,1,'Bachelor'),(2,1,'Master');
		UPDATE programs SET deleted_at='2026-01-01T00:00:00Z' WHERE id=3;
		INSERT INTO modules(id,program_id,name,deleted_at) VALUES(100,2,'Datenbanken 01',NULL),(101,1,'Gelöschtes Modul','2026-01-01T00:00:00Z'),(102,3,'Alter Studiengang',NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		if _, err := db.Exec("INSERT INTO modules(id,program_id,name) VALUES(?,1,?)", i, fmt.Sprintf("Datenbanken %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	seedPublicRefs(t, db)
	program := publicRef(t, db, "study_programs", "1")
	query := url.Values{"search": {"  DATENBANKEN  "}, "program": {program}, "degree": {"bachelor"}}
	status, body, _ := b.get("/admin/modules?" + query.Encode())
	if status != 200 || !strings.Contains(body, "25 Module gefunden") || !strings.Contains(body, "Datenbanken 01") || strings.Contains(body, "Datenbanken 21") || strings.Contains(body, ">Master</span>") {
		t.Fatal("module search did not combine program and degree, or page size is wrong")
	}
	if strings.Count(body, ">Informatik</option>") != 1 {
		t.Fatal("canonical study-program filter duplicated")
	}
	link := "/admin/modules?degree=bachelor&page=2&program=" + program + "&search=DATENBANKEN"
	if !strings.Contains(body, html.EscapeString(link)) {
		t.Fatal("pagination lost filters")
	}
	if status, body, _ := b.get(link); status != 200 || !strings.Contains(body, "Datenbanken 21") || strings.Contains(body, "Datenbanken 01") {
		t.Fatal("second page missing")
	}
	if status, _, _ := b.get(strings.Replace(link, "page=2", "page=3", 1)); status != 404 {
		t.Fatal("out-of-range page accepted")
	}
	if status, body, _ := b.get("/admin/modules"); status != 200 || !strings.Contains(body, "26 Module gefunden") || strings.Contains(body, "Gelöschtes Modul") || strings.Contains(body, "Alter Studiengang") {
		t.Fatal("lost modules without uploads or included deleted associations")
	}
	if status, body, _ := b.get("/admin/modules?search=unbekannt"); status != 200 || !strings.Contains(body, "Für diese Suche wurden keine Module gefunden.") {
		t.Fatal("empty filtered state missing")
	}
	if _, err := db.Exec("UPDATE users SET role='editor' WHERE id=900"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := b.get("/admin/modules"); status != 200 {
		t.Fatal("editor could not access module list")
	}
}
