package site

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/model/users"
)

func TestMemberInvitationRegistrationJourney(t *testing.T) {
	b, db := identityBrowser(t)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES (42,'admin@fsv-wh.de','Admin','unused','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	session, err := users.NewService(db, "http://example.org", users.DomainPolicy{Registration: []string{"studmail.w-hs.de"}, Accounts: []string{"studmail.w-hs.de", "fsv-wh.de"}}).CreateSession(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	status, body, _ := b.get("/admin/members/create")
	if status != 200 || strings.Contains(body, "Aufgabe im Vorstand") {
		t.Fatal("invitation form unavailable or still asks for board role")
	}
	csrf := csrfFrom(t, body)
	if status, _, _ := b.post("/admin/members/create", url.Values{"csrf": {csrf}, "name": {"Neue Person"}, "email": {"new@studmail.w-hs.de"}}); status != 303 {
		t.Fatal("invitation failed")
	}
	if status, body, _ := b.get("/admin/members"); status != 200 || !strings.Contains(body, "new@studmail.w-hs.de") || !strings.Contains(body, "Eingeladen") {
		t.Fatal("pending invitation not visible")
	}
	var link string
	if err := db.QueryRow(`SELECT link FROM mail_outbox WHERE recipient='new@studmail.w-hs.de'`).Scan(&link); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	invitee := newAdminBrowser(t, b.server)
	status, body, _ = invitee.get(target.RequestURI())
	if status != 200 {
		t.Fatal("invitation registration unavailable")
	}
	var verified int
	if err := db.QueryRow(`SELECT verified FROM users WHERE email='new@studmail.w-hs.de'`).Scan(&verified); err != nil || verified != 0 {
		t.Fatal("GET consumed invitation")
	}
	form := url.Values{"csrf": {csrfFrom(t, body)}, "token": {target.Query().Get("token")}, "password": {"secure 🔐🔐🔐"}}
	if status, _, h := invitee.post("/invitation/accept", form); status != 303 || h.Get("Location") != "/invitation/accept" {
		t.Fatal("invitation confirmation failed")
	}
	if status, body, _ := invitee.get("/invitation/accept"); status != 200 || !strings.Contains(body, "Dein Konto ist bestätigt") {
		t.Fatal("invitation receipt missing")
	}
	_, body, _ = invitee.get("/login")
	if status, _, headers := invitee.post("/login", url.Values{"csrf": {csrfFrom(t, body)}, "email": {"new@studmail.w-hs.de"}, "password": {"secure 🔐🔐🔐"}}); status != 303 || headers.Get("Location") != "/admin" {
		t.Fatal("confirmed member could not log in as reviewer")
	}
	if status, _, _ := invitee.get("/admin/submissions"); status != 200 {
		t.Fatal("reviewer cannot access submissions")
	}
	for _, path := range []string{"/admin/users", "/admin/members/create"} {
		if status, _, _ := invitee.get(path); status != 403 {
			t.Fatalf("invitation granted administration of %s", path)
		}
	}
	if status, _, _ := invitee.get(target.RequestURI()); status != 422 {
		t.Fatal("consumed invitation remained usable")
	}
}

func TestExistingMemberInvitationAndAccountEditing(t *testing.T) {
	b, db := identityBrowser(t)
	memberPath := "/admin/members/" + publicRef(t, db, "members", "1") + "/edit"
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES (42,'admin@fsv-wh.de','Admin','unused','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	session, err := users.NewService(db, "http://example.org", users.DomainPolicy{Registration: []string{"studmail.w-hs.de"}, Accounts: []string{"studmail.w-hs.de", "fsv-wh.de"}}).CreateSession(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	status, body, _ := b.get(memberPath)
	if status != 200 || !strings.Contains(body, "E-Mail-Adresse des Benutzerkontos") {
		t.Fatal("existing member cannot be linked to an account")
	}
	csrf := csrfFrom(t, body)
	if status, _, _ := b.post(memberPath, url.Values{"csrf": {csrf}, "name": {"Linked Member"}, "email": {"linked@studmail.w-hs.de"}}); status != 303 {
		t.Fatal("existing member invitation failed")
	}
	var userID int64
	if err := db.QueryRow(`SELECT user_id FROM people WHERE id='1'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM people WHERE user_id=?`, userID).Scan(&count); err != nil || count != 1 {
		t.Fatal("invitation duplicated the membership")
	}
	if status, body, _ := b.get("/team"); status != 200 || !strings.Contains(body, "Linked Member") {
		t.Fatal("pending confirmation hid the public member")
	}
	status, body, _ = b.get(memberPath)
	accountPath := "/admin/users/" + publicRef(t, db, "users", fmt.Sprint(userID))
	if status != 200 || !strings.Contains(body, accountPath) || strings.Contains(body, `name="email"`) {
		t.Fatal("linked member is not using the account identity")
	}
	if status, _, _ := b.post(memberPath, url.Values{"csrf": {csrfFrom(t, body)}, "name": {"Updated Member"}, "email": {"unexpected@studmail.w-hs.de"}}); status != 303 {
		t.Fatal("linked member name edit failed")
	}
	if status, body, _ := b.get(accountPath); status != 200 || strings.Contains(body, "Updated Member") || !strings.Contains(body, "Linked Member") || !strings.Contains(body, "linked@studmail.w-hs.de") || !strings.Contains(body, `href="`+memberPath+`"`) {
		t.Fatal("public name edit changed account identity or linkage")
	}
	if status, body, _ := b.get("/team"); status != 200 || !strings.Contains(body, "Updated Member") {
		t.Fatal("public name edit not shown")
	}
}
