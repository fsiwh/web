package site

import (
	"fmt"
	"html"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/urls"
)

func TestAdminRegulationListAndSourceDetails(t *testing.T) {
	handler, db := testApplication(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	if status, _, h := b.get(urls.AdminRegulationsPath); status != 303 || h.Get("Location") != "/login" {
		t.Fatal("anonymous access to PO list")
	}
	b.login()
	if _, err := db.Exec(`INSERT INTO programs(id,name) VALUES(1,'Informatik (B. Sc.)');
		INSERT INTO program_versions(program_id,name) VALUES(1,'2017')`); err != nil {
		t.Fatal(err)
	}
	for year := 2020; year <= 2044; year++ {
		if _, err := db.Exec("INSERT INTO program_versions(program_id,name) VALUES(1,?)", fmt.Sprint(year)); err != nil {
			t.Fatal(err)
		}
	}
	seedPublicRefs(t, db)
	program := publicRef(t, db, "programs", "1")
	status, body, _ := b.get("/admin/regulations?search=20&program=Informatik&degree=bachelor")
	if status != 200 || !strings.Contains(body, "26 Prüfungsordnungen gefunden") || !strings.Contains(body, html.EscapeString("/admin/regulations?degree=bachelor&page=2&program=Informatik&search=20")) {
		t.Fatal("list or pagination context missing")
	}
	if status, body, _ := b.get("/admin/regulations?degree=bachelor&page=2&program=Informatik&search=20"); status != 200 || !strings.Contains(body, "BPO_IN_2023.pdf") || !strings.Contains(body, "MHB_2023_IN_BA.pdf") {
		t.Fatal("second page lost matching PDFs")
	}
	if status, _, _ := b.get("/admin/regulations?page=3"); status != 404 {
		t.Fatal("out-of-range page accepted")
	}
	if status, body, _ := b.get(urls.AdminRegulation(program, "2023")); status != 200 || !strings.Contains(body, "Änderungssatzungen") || !strings.Contains(body, "Rahmenprüfungsordnung") || !strings.Contains(body, "09.10.2026") {
		t.Fatalf("source details: status=%d amendments=%t framework=%t handbook date=%t", status, strings.Contains(body, "Änderungssatzungen"), strings.Contains(body, "Rahmenprüfungsordnung"), strings.Contains(body, "09.10.2026"))
	}
	if status, body, _ := b.get(urls.AdminRegulation(program, "2017")); status != 200 || !strings.Contains(body, "noch keine geprüften Dokumentlinks") || strings.Contains(body, "BPO_IN_2023.pdf") {
		t.Fatal("unknown PO silently inherited documents")
	}
	if status, _, _ := b.get(urls.AdminRegulation(program, "2099")); status != 404 {
		t.Fatal("unknown PO details accepted")
	}
	if _, err := db.Exec("UPDATE users SET role='editor' WHERE id=900"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{urls.AdminRegulationsPath, urls.AdminRegulation(program, "2023")} {
		if status, _, _ := b.get(path); status != 200 {
			t.Fatal("editor could not access PO directory", path)
		}
	}
}
