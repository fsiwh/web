package site

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/model/organization"
)

func TestEditorContentAccessDoesNotGrantAccountAdministration(t *testing.T) {
	c, db := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	if _, err := db.Exec(`UPDATE users SET role='editor' WHERE id=900; UPDATE user_functions SET user_id=42 WHERE function_id='1'`); err != nil {
		t.Fatal(err)
	}
	member := publicRef(t, db, "users", "42")
	user := publicRef(t, db, "users", "42")
	for _, path := range []string{"/admin", "/admin/roles", "/admin/team/" + member + "/edit"} {
		status, body, _ := b.get(path)
		if status != 200 {
			t.Fatal("editor content page denied", path, status)
		}
		if strings.Contains(body, "/admin/users/") || strings.Contains(body, `href="/admin/members/create"`) || strings.Contains(body, `name="email"`) {
			t.Fatal("account controls leaked", path)
		}
	}
	if status, _, _ := b.post("/admin/team/"+member+"/edit", url.Values{"csrf": {b.csrf}, "name": {"Public Team Name"}, "email": {"attacker@example.org"}, "UserID": {"900"}, "role": {"admin"}}); status != 303 {
		t.Fatal("public profile save failed", status)
	}
	var accountName, accountEmail, role string
	var linked int
	db.QueryRow("SELECT name,email FROM users WHERE id=42").Scan(&accountName, &accountEmail)
	db.QueryRow("SELECT user_id FROM user_functions WHERE function_id='1'").Scan(&linked)
	db.QueryRow("SELECT role FROM users WHERE id=900").Scan(&role)
	if accountName == "Public Team Name" || accountEmail == "attacker@example.org" || linked != 42 || role != "editor" {
		t.Fatal("public profile changed account")
	}
	if status, body, _ := b.get("/team"); status != 200 || !strings.Contains(body, "Public Team Name") {
		t.Fatal("public name not persisted")
	}
	for _, path := range []string{"/admin/users", "/admin/users/" + user, "/admin/users/" + user + "/reset", "/admin/users/create"} {
		if status, _, _ := b.get(path); status != 403 {
			t.Fatal("editor account access", path, status)
		}
	}
	for _, path := range []string{"/admin/users/create", "/admin/users/" + user + "/edit", "/admin/users/" + user + "/confirm", "/admin/users/" + user + "/reset"} {
		if status, _, _ := b.post(path, url.Values{"csrf": {b.csrf}}); status != 403 {
			t.Fatal("editor account mutation", path, status)
		}
	}
	for _, part := range []string{"identity", "legal"} {
		path := "/admin/settings/" + part
		if status, body, _ := b.get(path); status != 200 || strings.Contains(body, `id="admin-form"`) {
			t.Fatal("restricted settings not read-only", part)
		}
		if status, _, _ := b.post(path, url.Values{"csrf": {b.csrf}, "Name": {"Forbidden"}}); status != 403 {
			t.Fatal("protected settings accepted", part)
		}
	}
	if status, _, _ := b.post("/admin/settings/links", url.Values{"csrf": {b.csrf}, "SocialLinksTitle": {"Editorial"}, "SocialLinksURL": {"https://example.org/editorial"}, "Name": {"Forbidden"}}); status != 303 {
		t.Fatal("editor links save failed", status)
	}
	if c.organization.Current().Name == "Forbidden" {
		t.Fatal("forged identity field saved")
	}
	d := c.organization.Current()
	d.Office = "Editorial office"
	if fields, err := c.organization.Save(context.Background(), 900, d); err != nil || len(fields) > 0 {
		t.Fatal("editor contact save rejected", fields, err)
	}
	d.HostingName = "Forbidden operator"
	if _, err := c.organization.Save(context.Background(), 900, d); err != organization.ErrForbidden {
		t.Fatal("model allowed legal mutation", err)
	}
}

func TestEditorActivitiesExcludeAccountsSecurityAndDrafts(t *testing.T) {
	c, db := testApplication(t)
	server := httptest.NewServer(c)
	defer server.Close()
	b := newAdminBrowser(t, server)
	b.login()
	if _, err := db.Exec(`UPDATE users SET role='editor' WHERE id=900; UPDATE users SET name='Private Account Name',email='private-account@example.org' WHERE id=42`); err != nil {
		t.Fatal(err)
	}
	store := audit.NewStore(db)
	for i, event := range []string{"account.login", "session.created", "submission.draft_saved", "content.unapproved_future_event"} {
		if err := store.Record(context.Background(), audit.Record{Time: time.Now(), Event: event, Outcome: "security-only-outcome", ActorUserID: 42, UserID: 42, Details: []slog.Attr{slog.String("content_label", "Private draft title"), slog.String("content_url", "/admin/users/private")}}); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	ctx := audit.WithRecorder(context.Background(), store)
	for i := 0; i < 21; i++ {
		audit.Content(ctx, "content.news_saved", "Editorial news", "/news")
	}
	for _, query := range []string{"", "?event=account.login&outcome=security-only-outcome"} {
		status, body, _ := b.get("/admin/audit" + query)
		if status != 200 || !strings.Contains(body, "21 Aktivitäten") || !strings.Contains(body, "Editorial news") {
			t.Fatal("editorial list missing", status)
		}
		for _, secret := range []string{"Private Account Name", "private-account@example.org", "Private draft title", "account.login", "session.created", "security-only-outcome", "unapproved_future_event", "/admin/users/", "Durch:", ">Konto<"} {
			if strings.Contains(body, secret) {
				t.Fatal("activity leak", secret)
			}
		}
	}
	for _, query := range []string{"Private+Account+Name", "private-account%40example.org", "Private+draft+title"} {
		if status, body, _ := b.get("/admin/audit?search=" + query); status != 200 || !strings.Contains(body, "0 Aktivitäten") {
			t.Fatal("search leaked excluded account or draft", query, status)
		}
	}
	if status, body, _ := b.get("/admin/audit?page=2"); status != 200 || !strings.Contains(body, "Editorial news") {
		t.Fatal("editorial pagination failed")
	}
	if status, _, _ := b.get("/admin/audit?page=3"); status != 404 {
		t.Fatal("editor pagination included hidden rows")
	}
}
