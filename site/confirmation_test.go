package site

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/model/users"
)

func TestResendAccountConfirmationAndMemberInvitation(t *testing.T) {
	b, db := identityBrowser(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES
		(42,'admin@fsv-wh.de','Admin','unchanged','admin',1,1),
		(43,'student@studmail.w-hs.de','Student','unchanged','user',1,0),
		(44,'member@studmail.w-hs.de','Member','unchanged','editor',1,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET invitation_pending=1 WHERE id=44`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	identity := users.NewService(db, "https://example.org", users.DomainPolicy{Registration: []string{"studmail.w-hs.de"}, Accounts: []string{"studmail.w-hs.de", "fsv-wh.de"}})
	session, err := identity.CreateSession(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	for _, entry := range []struct{ id, label, path string }{{"43", "Bestätigung erneut senden", "/email/confirm"}, {"44", "Einladung erneut senden", "/invitation/accept"}} {
		path := "/admin/users/" + publicRef(t, db, "users", entry.id)
		status, body, _ := b.get(path)
		if status != 200 || !strings.Contains(body, entry.label) || !strings.Contains(body, "Passwort zurücksetzen") {
			t.Fatal("account resend action missing")
		}
		csrf := csrfFrom(t, body)
		action := path + "/confirm"
		if status, _, _ := b.post(action, url.Values{}); status != 403 {
			t.Fatal("resend accepted without CSRF")
		}
		var previous string
		for i := 0; i < 2; i++ {
			if status, _, headers := b.post(action, url.Values{"csrf": {csrf}}); status != 303 || headers.Get("Location") != path {
				t.Fatal("resend failed")
			}
			var link string
			if err := db.QueryRow(`SELECT link FROM mail_outbox WHERE user_id=? AND kind='verify'`, entry.id).Scan(&link); err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(link)
			if err != nil || u.Path != entry.path {
				t.Fatal("incorrect confirmation journey")
			}
			current := u.Query().Get("token")
			if current == previous {
				t.Fatal("resend did not rotate token")
			}
			if i == 1 {
				if err := identity.ConsumeToken(ctx, previous, "verify", ""); !errors.Is(err, users.ErrToken) {
					t.Fatal("old confirmation link remained valid")
				}
			}
			previous = current
		}
		var password string
		var verified int
		if err := db.QueryRow(`SELECT password,verified FROM users WHERE id=?`, entry.id).Scan(&password, &verified); err != nil || password != "unchanged" || verified != 0 {
			t.Fatal("resend changed account credentials or confirmation")
		}
		if entry.path == "/email/confirm" {
			if err := identity.ConsumeToken(ctx, previous, "verify", ""); err != nil {
				t.Fatal(err)
			}
			if _, body, _ := b.get(path); strings.Contains(body, entry.label) {
				t.Fatal("verified account still offers confirmation resend")
			}
		} else {
			if _, err := identity.Invitation(ctx, previous); err != nil {
				t.Fatal("resent member invitation cannot be opened")
			}
		}
	}
	if status, _, _ := b.post("/admin/users/999/confirm", url.Values{"csrf": {session.CSRF}}); status != 404 {
		t.Fatal("unknown account accepted")
	}
	reviewer, err := identity.CreateSession(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET role='editor' WHERE id=42`); err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: reviewer.ID, Path: "/"}})
	if status, _, _ := b.post("/admin/users/"+publicRef(t, db, "users", "43")+"/confirm", url.Values{"csrf": {reviewer.CSRF}}); status != 403 {
		t.Fatal("reviewer can resend account confirmations")
	}
}
