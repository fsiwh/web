package site

import (
	"fmt"
	"html"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminStudyProgramsGroupsQualificationsAndPaginates(t *testing.T) {
	handler, db := testApplication(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	if status, _, headers := b.get("/admin/programs"); status != 303 || headers.Get("Location") != "/login" {
		t.Fatal("anonymous access to study-program management")
	}
	b.login()
	if status, body, _ := b.get("/admin/programs"); status != 200 || !strings.Contains(body, "Es sind noch keine Studiengänge vorhanden.") {
		t.Fatal("empty list missing")
	}
	if _, err := db.Exec(`INSERT INTO study_programs(id,name) VALUES(100,'Informatik');
		INSERT INTO programs(id,name,deleted_at) VALUES(100,'Informatik B',NULL),(101,'Informatik M',NULL),(102,'Informatik alt','2026-01-01T00:00:00Z');
		INSERT INTO program_qualifications(legacy_program_id,study_program_id,degree) VALUES(100,100,'Bachelor'),(101,100,'Master'),(102,100,'');
		INSERT INTO modules(id,program_id,name,deleted_at) VALUES(100,100,'Datenbanken',NULL),(101,101,'Datenbanken',NULL),(102,101,'Entfernt','2026-01-01T00:00:00Z'),(103,102,'Veraltet',NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		if _, err := db.Exec("INSERT INTO study_programs(id,name) VALUES(?,?)", i, fmt.Sprintf("Studiengang %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	status, body, _ := b.get("/admin/programs?search=%20INFORMATIK%20&degree=master")
	if status != 200 || !strings.Contains(body, "1 Studiengang gefunden") || !strings.Contains(body, "Bachelor, Master") || !strings.Contains(body, ">2</span>") {
		t.Fatal("qualifications were not grouped, search failed or deleted associations were counted")
	}
	if status, body, _ := b.get("/admin/programs?search=Studiengang"); status != 200 || !strings.Contains(body, "25 Studiengänge gefunden") || !strings.Contains(body, "Studiengang 01") || strings.Contains(body, "Studiengang 21") || !strings.Contains(body, ">0</span>") || !strings.Contains(body, html.EscapeString("/admin/programs?page=2&search=Studiengang")) {
		t.Fatal("programs without modules or pagination context missing")
	}
	if status, body, _ := b.get("/admin/programs?page=2&search=Studiengang"); status != 200 || !strings.Contains(body, "Studiengang 21") || strings.Contains(body, "Studiengang 01") {
		t.Fatal("second page incorrect")
	}
	if status, _, _ := b.get("/admin/programs?page=3"); status != 404 {
		t.Fatal("out-of-range page accepted")
	}
	if status, body, _ := b.get("/admin/programs?search=unbekannt"); status != 200 || !strings.Contains(body, "Für diese Suche wurden keine Studiengänge gefunden.") {
		t.Fatal("empty search state missing")
	}
	if _, err := db.Exec("UPDATE users SET role='editor' WHERE id=900"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := b.get("/admin/programs"); status != 200 {
		t.Fatal("editor could not access study-program list")
	}
}
