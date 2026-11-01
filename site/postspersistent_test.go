package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/view"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/model/users"
	xhtml "golang.org/x/net/html"
)

func TestPersistentPostsRequests(t *testing.T) {
	path := testdb.Path(t)
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO users(id,email,name,password,active,verified) VALUES(1,'author@council.example.invalid','Echter Autor','unused',1,1),(2,'other@council.example.invalid','Andere Person','unused',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	authorRef := publicRef(t, db, "users", "1")
	otherRef := publicRef(t, db, "users", "2")
	views, err := view.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	c := &Handler{views: views, posts: posts.NewService(db)}
	request := func(method, path string, form url.Values, user int64, handle http.HandlerFunc, post, comment string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("post", post)
		r.SetPathValue("comment", comment)
		w := httptest.NewRecorder()
		response := &sessionResponse{ResponseWriter: w}
		if user != 0 {
			response.session = &session{Authenticated: true, UserID: user, CSRF: "forum-token"}
		}
		handle(response, r)
		return w
	}
	if w := request("POST", "/posts/create", nil, 0, c.EditDiscussion, "", ""); w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatal(w)
	}
	form := url.Values{"title": {"Ein echter Beitrag"}, "body": {"Ein ausreichender Text."}, "type": {"discussion"}, "rich_body": {`<p><strong>Ein ausreichender Text.</strong><script>bad()</script></p>`}}
	w := request("POST", "/posts/create", form, 1, c.EditDiscussion, "", "")
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/posts/") {
		t.Fatal(w)
	}
	post := strings.TrimPrefix(w.Header().Get("Location"), "/posts/")
	thread, err := c.posts.Lookup(context.Background(), post)
	if err != nil || thread.Author != "Echter Autor" || strings.Contains(thread.Body, "bad()") {
		t.Fatalf("%+v %v", thread, err)
	}
	if w = request("GET", "/posts/"+post+"/edit", nil, 2, c.EditDiscussion, post, ""); w.Code != 403 {
		t.Fatal(w)
	}
	if w = request("POST", "/posts/"+post+"/edit", form, 2, c.EditDiscussion, post, ""); w.Code != 403 {
		t.Fatal(w)
	}
	if w = request("GET", "/posts/"+post+"/edit", nil, 1, c.EditDiscussion, post, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `name="csrf" value="forum-token"`) {
		t.Fatal(w)
	}
	form.Set("title", "")
	if w = request("POST", "/posts/"+post+"/edit", form, 1, c.EditDiscussion, post, ""); w.Code != 422 {
		t.Fatal(w)
	}
	if w = request("POST", "/posts/"+post+"/comments", url.Values{"text": {""}}, 1, c.Comment, post, ""); w.Code != 422 {
		t.Fatal(w)
	}
	w = request("POST", "/posts/"+post+"/comments", url.Values{"text": {"<script>Kein ausführbarer Kommentar</script>"}}, 2, c.Comment, post, "")
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "#comment-") {
		t.Fatal(w)
	}
	w = request("GET", "/posts/"+post, nil, 2, c.Discussion, post, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>Kein") || !strings.Contains(w.Body.String(), "&lt;script&gt;Kein") || !strings.Contains(w.Body.String(), "Kommentar bearbeiten") {
		t.Fatal(w)
	}
	list, err := c.posts.Find(context.Background(), posts.Filter{})
	if err != nil || len(list) != 1 || list[0].Comments != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	t.Run("public author profiles protect private identities", func(t *testing.T) {
		profileHandler := &Handler{views: c.views, posts: c.posts, identity: users.NewService(db, "http://example.test", users.DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}})}
		if _, err := db.Exec(`UPDATE users SET private=0 WHERE id=1; UPDATE users SET private=1 WHERE id=2;
		INSERT INTO discussion_posts(id,title,body,user_id,type,deleted_at) VALUES(999001,'Nicht veröffentlichter Beitrag','Hidden body',1,'discussion','2026-01-01T00:00:00Z');`); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`UPDATE users SET private=0 WHERE id=2; DELETE FROM discussion_posts WHERE id=999001`)
		for _, tc := range []struct {
			id   string
			code int
		}{{authorRef, 200}, {otherRef, 404}, {publicid.New(), 404}, {"999", 404}, {"1", 404}, {"2", 404}, {"01", 404}} {
			r := httptest.NewRequest("GET", "/users/"+tc.id, nil)
			r.SetPathValue("id", tc.id)
			w := httptest.NewRecorder()
			profileHandler.PublicProfile(w, r)
			if w.Code != tc.code {
				t.Fatalf("profile %s: %d", tc.id, w.Code)
			}
			if strings.Contains(w.Body.String(), "@council.example.invalid") || strings.Contains(w.Body.String(), "Nicht veröffentlichter Beitrag") {
				t.Fatal("profile leaked non-public data")
			}
			if tc.code == 200 && (!strings.Contains(w.Body.String(), "Echter Autor") || !strings.Contains(w.Body.String(), "Ein echter Beitrag")) {
				t.Fatal("public profile missing author or posts")
			}
		}
		thread, err := c.posts.Lookup(context.Background(), post)
		if err != nil || !thread.AuthorPublic || thread.AuthorID != 1 || thread.Replies[0].AuthorPublic || thread.Replies[0].Author != "Anonym" {
			t.Fatalf("author visibility: %+v %v", thread, err)
		}
		w := request("GET", "/posts/"+post, nil, 0, c.Discussion, post, "")
		if strings.Contains(w.Body.String(), `href="/users/`) {
			t.Fatal("public profile links must not be exposed")
		}
		if _, err := db.Exec(`UPDATE users SET deleted_at='2026-01-01T00:00:00Z' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`UPDATE users SET deleted_at=NULL WHERE id=1`)
		r := httptest.NewRequest("GET", "/users/"+authorRef, nil)
		r.SetPathValue("id", authorRef)
		w = httptest.NewRecorder()
		profileHandler.PublicProfile(w, r)
		if w.Code != 404 {
			t.Fatal("deleted profile is public")
		}
	})
	t.Run("comment errors retain escaped text and resolve the edit page", func(t *testing.T) {
		ctx := context.Background()
		postID, err := c.posts.Save(ctx, 1, "", posts.Draft{Title: "Kommentarprüfung", Body: "Eine Diskussion zur Fehlerbehandlung.", Type: "discussion"}, "")
		if err != nil {
			t.Fatal(err)
		}
		var commentID string
		for range 21 {
			commentID, err = c.posts.SaveComment(ctx, 1, postID, "", "Unveränderter Kommentar")
			if err != nil {
				t.Fatal(err)
			}
		}
		text := "<script>unerlaubt()</script>" + strings.Repeat("ü", 10001)
		for _, edit := range []bool{false, true} {
			path, id, anchor := "/posts/"+postID+"/comments", "", "comment-text"
			if edit {
				path += "/" + commentID + "/edit"
				id, anchor = commentID, "edit-comment-"+commentID
			}
			w := request("POST", path, url.Values{"text": {text}}, 1, c.Comment, postID, id)
			body := w.Body.String()
			if w.Code != 422 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !htmlContains(body, "<title>Fehler: Kommentarprüfung") || !strings.Contains(body, `href="#`+anchor+`"`) || strings.Contains(body, "<script>unerlaubt()") {
				t.Fatalf("invalid comment recovery: %d %s", w.Code, body)
			}
			doc, err := xhtml.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			field := postsTestElement(t, doc, "textarea", "id", anchor)
			if got := postsTestText(field); got != text {
				t.Fatal("invalid input was not retained verbatim")
			}
			if edit && !strings.Contains(body, `id="comment-`+commentID+`"`) {
				t.Fatal("edit recovery did not load the comment's page")
			}
		}
		w := request("POST", "/posts/"+postID+"/comments/"+commentID+"/edit", url.Values{"text": {text}}, 2, c.Comment, postID, commentID)
		if w.Code != 403 || strings.Contains(w.Body.String(), "unerlaubt()") {
			t.Fatal("validation bypassed comment ownership")
		}
		thread, _, err := c.posts.LookupPage(ctx, postID, 2, 1)
		if err != nil || len(thread.Replies) != 1 || thread.Replies[0].Text != "Unveränderter Kommentar" {
			t.Fatalf("failed validation modified stored comments: %+v %v", thread, err)
		}
	})
	t.Run("voting authentication csrf and local return", func(t *testing.T) {
		c.identity = users.NewService(db, "https://example.org", users.DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}})
		session, err := c.identity.CreateSession(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		router := http.NewServeMux()
		router.HandleFunc("POST /posts/{post}/vote", c.Vote)
		router.HandleFunc("POST /posts/{post}/comments/{comment}/vote", c.Vote)
		handler := c.withSession(routePolicy{authenticated: true, csrf: true, bodyLimit: 8 << 20}, router)
		for _, tc := range []struct {
			cookie, token, value, back string
			code                       int
			location                   string
		}{
			{"", session.CSRF, "1", "/", 303, "/login"},
			{session.ID, "wrong", "1", "/", 403, ""},
			{session.ID, session.CSRF, "2", "/", 422, ""},
			{session.ID, session.CSRF, "1", "https://example.org/", 303, "/posts/" + post},
			{session.ID, session.CSRF, "-1", "//example.org/", 303, "/posts/" + post},
			{session.ID, session.CSRF, "0", "/#home-forum", 303, "/#home-forum"},
		} {
			form := url.Values{"csrf": {tc.token}, "vote": {tc.value}, "return": {tc.back}}
			r := httptest.NewRequest("POST", "/posts/"+post+"/vote", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.code || w.Header().Get("Location") != tc.location {
				t.Fatalf("%+v: %d %s", tc, w.Code, w.Header().Get("Location"))
			}
		}
		current, err := c.posts.Lookup(context.Background(), post)
		if err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"/vote", "/comments/" + current.Replies[0].ID + "/vote"} {
			form := url.Values{"csrf": {session.CSRF}, "vote": {"1"}}
			r := httptest.NewRequest("POST", "/posts/"+post+suffix, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Accept", "application/json")
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session.ID})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"score":1,"selected":1}` {
				t.Fatalf("JSON vote: %d %s", w.Code, w.Body.String())
			}
		}
	})
	for _, text := range []string{
		"Literal &lt;script&gt; und &amp; bleiben erhalten.",
		"https://example.org/?value=&lt;script&gt;&amp;next=1",
		"<script>Kein HTML</script> javascript:alert(1)",
	} {
		t.Run(text, func(t *testing.T) {
			post, err := c.posts.Save(context.Background(), 1, "", posts.Draft{Title: "Klartext prüfen", Body: text, Type: "discussion"}, "")
			if err != nil {
				t.Fatal(err)
			}
			comment, err := c.posts.SaveComment(context.Background(), 1, post, "", text)
			if err != nil {
				t.Fatal(err)
			}
			w := request("GET", "/posts/"+post, nil, 1, c.Discussion, post, "")
			if w.Code != 200 {
				t.Fatal(w)
			}
			doc, err := xhtml.Parse(strings.NewReader(w.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			commentNode := postsTestElement(t, doc, "li", "id", "comment-"+comment)
			shownComment := postsTestElement(t, commentNode, "p", "class", "govuk-body")
			editComment := postsTestElement(t, commentNode, "textarea", "name", "text")
			postNode := postsTestElement(t, doc, "article", "aria-label", "Beitrag")
			shownPost := postsTestElement(t, postNode, "p", "class", "govuk-body")
			for _, node := range []*xhtml.Node{shownComment, editComment, shownPost} {
				got := postsTestText(node)
				if node.Data == "p" {
					got = strings.Join(strings.Fields(got), " ")
				}
				if got != text {
					t.Fatalf("%s text = %q, want %q", node.Data, got, text)
				}
			}
			if strings.Contains(w.Body.String(), "<script>Kein HTML") {
				t.Fatal("literal text became executable HTML")
			}
			if strings.HasPrefix(text, "https://") {
				for _, node := range []*xhtml.Node{shownComment, shownPost} {
					postsTestElement(t, node, "a", "href", text)
				}
			}
			w = request("GET", "/posts/"+post+"/edit", nil, 1, c.EditDiscussion, post, "")
			if w.Code != 200 {
				t.Fatal(w)
			}
			doc, err = xhtml.Parse(strings.NewReader(w.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			if got := postsTestText(postsTestElement(t, doc, "textarea", "name", "body")); got != text {
				t.Fatalf("post edit text = %q, want %q", got, text)
			}
		})
	}
}

func postsTestElement(t *testing.T, root *xhtml.Node, tag, key, value string) *xhtml.Node {
	t.Helper()
	var find func(*xhtml.Node) *xhtml.Node
	find = func(node *xhtml.Node) *xhtml.Node {
		if node.Type == xhtml.ElementNode && node.Data == tag {
			for _, attr := range node.Attr {
				if attr.Key == key && attr.Val == value {
					return node
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if found := find(child); found != nil {
				return found
			}
		}
		return nil
	}
	if found := find(root); found != nil {
		return found
	}
	t.Fatalf("missing %s with %s=%q", tag, key, value)
	return nil
}

func postsTestText(node *xhtml.Node) string {
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(postsTestText(child))
	}
	return text.String()
}
