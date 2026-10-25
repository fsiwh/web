package site

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/datetime"
)

func TestSessionWarningAndExtensionPolicy(t *testing.T) {
	handler, db := testApplication(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	b := newAdminBrowser(t, server)
	if status, _, _ := b.get("/session"); status != 401 {
		t.Fatalf("anonymous status: %d", status)
	}
	b.login()
	_, body, _ := b.get("/admin")
	if !strings.Contains(body, `id="session-warning"`) || !strings.Contains(body, `aria-live="polite"`) {
		t.Fatal("accessible warning missing")
	}
	near := datetime.FormatUTC(time.Now().Add(time.Minute))
	if _, err := db.Exec("UPDATE web_sessions SET idle_expires_at=? WHERE user_id=900", near); err != nil {
		t.Fatal(err)
	}
	status, body, _ := b.get("/session")
	if status != 200 {
		t.Fatal("status unavailable")
	}
	var before struct{ Deadline, Absolute, Now int64 }
	if err := json.Unmarshal([]byte(body), &before); err != nil {
		t.Fatal(err)
	}
	if before.Deadline-before.Now > 61_000 {
		t.Fatal("background status extended session")
	}
	if status, _, _ := b.post("/session/extend", url.Values{"csrf": {"invalid"}}); status != 403 {
		t.Fatal("extension lacked CSRF protection")
	}
	status, body, _ = b.post("/session/extend", url.Values{"csrf": {b.csrf}})
	if status != 200 {
		t.Fatalf("extension failed: %d", status)
	}
	var after struct{ Deadline, Absolute, Now int64 }
	if err := json.Unmarshal([]byte(body), &after); err != nil {
		t.Fatal(err)
	}
	if after.Deadline-after.Now < 29*60_000 || after.Absolute != before.Absolute {
		t.Fatal("incorrect extension limits")
	}
	if _, err := db.Exec("UPDATE web_sessions SET idle_expires_at='2000-01-01T00:00:00Z' WHERE user_id=900"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := b.get("/session"); status != 401 {
		t.Fatal("idle expiry ignored")
	}
	if status, _, headers := b.get("/admin"); status != 303 || headers.Get("Location") != "/login" {
		t.Fatal("expired session retained admin access")
	}
	if status, body, _ := b.get("/session/expired"); status != 200 || !strings.Contains(body, "Deine Sitzung ist abgelaufen") || !strings.Contains(body, "Erneut anmelden") {
		t.Fatal("expiry page missing")
	}
}
