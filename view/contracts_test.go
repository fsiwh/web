package view

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestAllPageContracts(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for name := range renderer.pages {
		for _, signedIn := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/signed-in=%t", name, signedIn), func(t *testing.T) {
				t.Parallel()
				page := contractPage(name, signedIn)
				var out strings.Builder
				if err := renderer.RenderPage(&out, name, page); err != nil {
					t.Fatal(err)
				}
				body := out.String()
				if strings.Contains(body, "<no value>") || strings.Contains(body, "#ZgotmplZ") || strings.Contains(body, "<unsafe>") {
					t.Fatal("rendered missing data or unescaped content")
				}
				root, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				var main, title int
				var walk func(*html.Node)
				walk = func(n *html.Node) {
					if n.Type == html.ElementNode {
						if n.Data == "main" {
							main++
						}
						if n.Data == "title" {
							title++
						}
					}
					for child := n.FirstChild; child != nil; child = child.NextSibling {
						walk(child)
					}
				}
				walk(root)
				if main != 1 || title != 1 {
					t.Fatalf("expected one document title and main landmark, got %d and %d", title, main)
				}
				if !strings.Contains(body, name+" &lt;unsafe&gt;") {
					t.Fatal("page title missing or replaced by another page's template")
				}
			})
		}
	}
}

// Each fixture supplies only that route's feature state. This catches templates
// that accidentally reach into another feature's controller data.
func contractPage(name string, signedIn bool) Page {
	p := Page{
		Title: name + " <unsafe>", Active: name, SignedIn: signedIn, CSRF: "test-csrf",
		Organization: Organization{Name: "Testfachschaft", ShortName: "FS Test"},
	}
	post := Post{ID: "example", Title: "Beitrag <unsafe>", Body: "Ein Beitrag", Created: "2026-10-31T12:00:00Z"}
	switch name {
	case "landing":
		p.Active = "home"
		p.Home.Posts = []HomePost{HomeItem(post)}
		p.Home.Events = []Event{{ID: "event", Title: "Veranstaltung", EventDate: "2026-11-01T12:00:00Z"}}
	case "posts", "news", "profile":
		p.Community = true
		p.Posts = PostsPage{Posts: []Post{post}, CanWrite: signedIn}
	case "thread":
		p.Active, p.Community = "posts", true
		p.Posts = PostsPage{Thread: &Thread{Post: post, Replies: []Comment{{ID: "reply", Text: "Antwort", Created: post.Created}}}, CanWrite: signedIn}
	case "editor":
		p.Active, p.Community = "posts", true
		p.Editor = &ContentEditor{Action: "/posts", Draft: EditorDraft{Title: "Entwurf"}}
	case "events":
		p.Community = true
		p.Events = EventsPage{Upcoming: []Event{{ID: "event", Title: "Veranstaltung", EventDate: "2026-11-01T12:00:00Z"}}}
	case "exams", "module", "examsettings", "upload", "submission", "submissions":
		p.Active = "exams"
		if name == "module" {
			p.Exams.Module = &Module{Title: "Modul"}
		}
		if name == "submission" {
			p.Submission = &SubmissionPage{Entry: Submission{Status: "pending"}}
		}
	case "media":
		p.Community = true
	case "admin":
		p.Admin = &AdminPage{View: "index"}
		p.CanAdmin = true
	case "team", "about", "elections", "regulations", "content":
		p.About = true
		if name == "team" {
			p.Team = []TeamGroup{{ID: "project", Title: "Projekte"}}
		}
	case "sessionexpired":
		p.Active = "session-expired"
	}
	return p
}

func TestExplicitFragmentContracts(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := renderer.RenderFragment(&out, "exams", "results", contractPage("exams", false)); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 || strings.Contains(out.String(), "<!doctype") || strings.Contains(out.String(), "<head>") {
		t.Fatal("results fragment must contain only result markup")
	}
	for _, target := range []struct{ page, fragment string }{
		{"exams", "layout"}, {"exams", "sidebar"}, {"posts", "results"}, {"missing", "results"},
	} {
		out.Reset()
		if err := renderer.RenderFragment(&out, target.page, target.fragment, Page{}); err == nil || out.Len() != 0 {
			t.Fatalf("unexposed fragment %s/%s should fail before writing", target.page, target.fragment)
		}
	}
}
