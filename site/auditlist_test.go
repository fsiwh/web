package site

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
)

func TestAuditListRequiresAdministrator(t *testing.T) {
	handler, db := testApplication(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	if status, _, _ := b.get("/admin/audit"); status != 303 {
		t.Fatalf("anonymous status %d", status)
	}
	b.login()
	store := audit.NewStore(db)
	for i := 0; i < 21; i++ {
		if err := store.Record(context.Background(), audit.Record{Time: time.Date(2026, 10, 25, 12, 0, i, 0, time.UTC), Event: "account.login", Outcome: "success", ActorUserID: 900, UserID: 42}); err != nil {
			t.Fatal(err)
		}
	}
	actor := publicRef(t, db, "users", "900")
	subject := publicRef(t, db, "users", "42")
	status, body, headers := b.get("/admin/audit")
	if status != 200 || headers.Get("Cache-Control") != "no-store" || !strings.Contains(body, "Aktivitäten") || !strings.Contains(body, "account.login") || !strings.Contains(body, "success") || !strings.Contains(body, "/admin/users/"+actor) || !strings.Contains(body, "/admin/users/"+subject) || !strings.Contains(body, "2026-10-25T12:00:20Z") || strings.Contains(body, "2026-10-25T12:00:00Z") {
		t.Fatal("audit list lost metadata, links, ordering or pagination")
	}
	status, body, _ = b.get("/admin/audit?page=2")
	if status != 200 || !strings.Contains(body, "2026-10-25T12:00:00Z") || !strings.Contains(body, "Aktivitäten – Seite 2 von 2") {
		t.Fatal("second audit page missing")
	}
	for _, tc := range []struct {
		query  string
		status int
	}{{"?page=0", 400}, {"?page=999", 404}, {"?page=invalid", 400}} {
		if status, _, _ := b.get("/admin/audit" + tc.query); status != tc.status {
			t.Fatalf("%s: %d", tc.query, status)
		}
	}
	if _, err := db.Exec("UPDATE users SET deleted_at='2026-10-25T13:00:00Z' WHERE id=42"); err != nil {
		t.Fatal(err)
	}
	_, body, _ = b.get("/admin/audit")
	if !strings.Contains(body, "Gelöschtes Konto (42)") || strings.Contains(body, "/admin/users/"+subject) {
		t.Fatal("deleted account history lost or stale link retained")
	}
	for _, role := range []string{"editor", "user"} {
		if _, err := db.Exec("UPDATE users SET role=? WHERE id=900", role); err != nil {
			t.Fatal(err)
		}
		if status, body, _ := b.get("/admin/audit"); status != 403 || strings.Contains(body, "account.login") {
			t.Fatalf("%s can read audit history: %d", role, status)
		}
		_, body, _ = b.get("/admin")
		if strings.Contains(body, `href="/admin/audit"`) {
			t.Fatalf("%s sees audit navigation", role)
		}
	}
}

func TestAuditListEmpty(t *testing.T) {
	handler, _ := testApplication(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	status, body, _ := b.get("/admin/audit")
	if status != 200 || !strings.Contains(body, "Es wurden noch keine Aktivitäten protokolliert.") {
		t.Fatal("missing empty audit state")
	}
}

func TestAuditFiltersKeepContextAndCompactPresentation(t *testing.T) {
	handler, db := testApplication(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	store := audit.NewStore(db)
	for i := 0; i < 25; i++ {
		event, outcome := "account.login", "success"
		if i == 23 {
			event = "session.created"
		}
		if i == 24 {
			outcome = "incorrect_password"
		}
		if err := store.Record(context.Background(), audit.Record{Time: time.Date(2026, 10, 25, 12, 0, i, 0, time.UTC), Event: event, Outcome: outcome, ActorUserID: 42, UserID: 42}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/admin/audit?search=Kim&event=account.login&outcome=success"
	status, body, _ := b.get(path)
	for _, want := range []string{"23 Aktivitäten", `class="govuk-tag govuk-tag--green">Erfolgreich`, "Anmeldung", `event=account.login&amp;outcome=success&amp;page=2&amp;search=Kim`, `value="account.login" selected`, `value="success" selected`} {
		if status != 200 || !htmlContains(body, want) {
			t.Fatalf("missing filtered presentation %q (status %d)", want, status)
		}
	}
	if strings.Contains(body, "Durch:") || strings.Contains(body, "Nicht zugeordnet") || strings.Contains(body, "Betroffenes Konto") || strings.Contains(body, "<details") {
		t.Fatal("redundant account text retained")
	}
	status, body, _ = b.get(path + "&page=2")
	if status != 200 || !strings.Contains(body, "2026-10-25T12:00:00Z") {
		t.Fatal("filtered pagination lost history")
	}
	_, body, _ = b.get("/admin/audit?event=account.login&outcome=incorrect_password")
	if !strings.Contains(body, "1 Aktivität") || !htmlContains(body, `class="govuk-tag govuk-tag--red">Falsches Passwort`) {
		t.Fatal("failure filter or tag missing")
	}
	for search, total := range map[string]int{"kim@studmail.w-hs.de": 25, "Administrator": 0, "no-match": 0, "%27%20OR%201=1--": 0} {
		status, body, _ = b.get("/admin/audit?search=" + search)
		if status != 200 || !strings.Contains(body, fmt.Sprintf("%d Aktivitäten", total)) {
			t.Fatalf("unexpected search result for %q", search)
		}
	}
}
