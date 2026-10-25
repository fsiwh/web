package site

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/model/users"
)

func TestSubmissionMailSettingsAuthorizationAndPersistence(t *testing.T) {
	b, db := identityBrowser(t)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(1,'reviewer@fsi-wh.de','Reviewer','unused','user',1,1)`); err != nil {
		t.Fatal(err)
	}
	seedExamPublicIDs(t, db)
	session, err := users.NewService(db, "https://example.org", users.DomainPolicy{Accounts: []string{"fsi-wh.de"}}).CreateSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	if status, body, _ := b.get("/exams/settings"); status != 200 || strings.Contains(body, `name="submission_mail"`) {
		t.Fatal("ordinary account was offered reviewer notifications", status)
	}
	form := url.Values{"csrf": {session.CSRF}, "intent": {"submission-mail"}, "submission_mail": {"on"}}
	if status, _, _ := b.post("/exams/settings", form); status != 403 {
		t.Fatal("ordinary account subscribed to reviewer notifications", status)
	}
	if _, err := db.Exec(`UPDATE users SET role='editor' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	session, err = users.NewService(db, "https://example.org", users.DomainPolicy{Accounts: []string{"fsi-wh.de"}}).CreateSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	form.Set("csrf", session.CSRF)
	if status, body, _ := b.get("/exams/settings"); status != 200 || !strings.Contains(body, `name="submission_mail"`) {
		t.Fatal("reviewer notification setting is missing", status)
	}
	noCSRF := url.Values{"intent": {"submission-mail"}, "submission_mail": {"on"}}
	if status, _, _ := b.post("/exams/settings", noCSRF); status != 403 {
		t.Fatal("notification setting accepted missing CSRF", status)
	}
	if status, _, h := b.post("/exams/settings", form); status != 303 || h.Get("Location") != "/exams/settings" {
		t.Fatal("reviewer could not save preference", status)
	}
	var subscribed bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM submission_mail_subscriptions WHERE user_id=1)`).Scan(&subscribed); err != nil || !subscribed {
		t.Fatal("notification preference was not persisted", err)
	}
	if status, body, _ := b.get("/exams/settings"); status != 200 || !strings.Contains(body, "checked") {
		t.Fatal("enabled preference not rendered", status)
	}
	// Saving catalogue filters is independent of the notification form.
	if status, _, _ := b.post("/exams/settings", url.Values{"csrf": {session.CSRF}}); status != 303 {
		t.Fatal("could not save empty catalogue filters", status)
	}
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM submission_mail_subscriptions WHERE user_id=1)`).Scan(&subscribed); err != nil || !subscribed {
		t.Fatal("saving filters cleared notification preference", err)
	}
	form.Del("submission_mail")
	if status, _, _ := b.post("/exams/settings", form); status != 303 {
		t.Fatal("could not unsubscribe", status)
	}
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM submission_mail_subscriptions WHERE user_id=1)`).Scan(&subscribed); err != nil || subscribed {
		t.Fatal("unsubscribe was not persisted", err)
	}
}
