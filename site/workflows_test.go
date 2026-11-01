package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/model/users"
)

func TestLivePostsRoutingCSRFAndRestart(t *testing.T) {
	b, db := identityBrowser(t)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES (42,'author@students.example.invalid','Autor','unused','user',1,1),(43,'other@students.example.invalid','Andere Person','unused','user',1,1)`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	if status, _, headers := b.get("/posts/create"); status != 303 || headers.Get("Location") != "/login" {
		t.Fatal("anonymous forum writes are not protected")
	}
	identity := users.NewService(db, "https://example.org", users.DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}})
	session, err := identity.CreateSession(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: session.ID, Path: "/"}})
	status, body, _ := b.get("/posts/create")
	if status != 200 {
		t.Fatal("authenticated editor unavailable")
	}
	form := url.Values{"title": {"Dauerhafter Forumsbeitrag"}, "body": {"Ein vollständiger Beitragstext."}, "type": {"discussion"}}
	if status, _, _ := b.post("/posts/create", form); status != 403 {
		t.Fatal("forum creation accepts missing CSRF")
	}
	form.Set("csrf", csrfFrom(t, body))
	status, _, headers := b.post("/posts/create", form)
	if status != 303 || !strings.HasPrefix(headers.Get("Location"), "/posts/") {
		t.Fatal("forum creation failed")
	}
	post := headers.Get("Location")
	if !publicid.Valid(strings.TrimPrefix(post, "/posts/")) {
		t.Fatalf("post redirect exposed a non-public ID: %s", post)
	}
	var internalPost string
	if err := db.QueryRow(`SELECT id FROM discussion_posts WHERE title='Dauerhafter Forumsbeitrag'`).Scan(&internalPost); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"/posts/" + internalPost, "/posts/" + internalPost + "/edit"} {
		if status, _, headers := b.get(alias); status != 404 || headers.Get("Location") != "" {
			t.Fatalf("numeric alias accepted: %s (%d)", alias, status)
		}
	}
	if status, _, _ := b.post("/posts/"+internalPost+"/edit", form); status != 404 {
		t.Fatal("numeric post alias accepted a write")
	}
	if status, _, _ := b.post(post+"/comments", url.Values{"text": {"Ein dauerhafter Kommentar."}}); status != 403 {
		t.Fatal("comment creation accepts missing CSRF")
	}
	if status, _, _ := b.post(post+"/comments", url.Values{"text": {"Ein dauerhafter Kommentar."}, "csrf": {session.CSRF}}); status != 303 {
		t.Fatal("comment route is not connected")
	}
	other, err := identity.CreateSession(context.Background(), 43)
	if err != nil {
		t.Fatal(err)
	}
	b.client.Jar.SetCookies(mustURL(t, b.server.URL), []*http.Cookie{{Name: sessionCookie, Value: other.ID, Path: "/"}})
	if status, _, _ := b.get(post + "/edit"); status != 403 {
		t.Fatal("other account can edit a post")
	}
	form.Set("csrf", other.CSRF)
	if status, _, _ := b.post(post+"/edit", form); status != 403 {
		t.Fatal("other account can update a post")
	}
	handler, err := New(persistentServices(t, db, nil), false)
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(handler)
	defer restarted.Close()
	b.server = restarted
	if status, body, _ := b.get(post); status != 200 || !strings.Contains(body, "Dauerhafter Forumsbeitrag") || !strings.Contains(body, "Ein dauerhafter Kommentar.") {
		t.Fatal("restart lost the post or comment")
	}
	if status, body, _ := b.get("/"); status != 200 || !strings.Contains(body, "Dauerhafter Forumsbeitrag") || strings.Contains(body, "LaTeX Template") {
		t.Fatal("homepage still reads the embedded forum snapshot")
	}
	if _, err := db.Exec(`UPDATE users SET active=0 WHERE id=43`); err != nil {
		t.Fatal(err)
	}
	if status, _, headers := b.get("/posts/create"); status != 303 || headers.Get("Location") != "/login" {
		t.Fatal("inactive account can use the editor")
	}
}
