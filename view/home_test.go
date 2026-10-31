package view

import (
	"strings"
	"testing"
)

func TestLandingContentRendersWithoutPageState(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := renderer.pages["landing"].ExecuteTemplate(&out, "home/content", LandingContent{
		Organization: Organization{ShortName: "FS <Informatik>"},
	}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{
		"Deine FS &lt;Informatik&gt;", "Noch keine Veranstaltungen veröffentlicht.",
		"Noch keine Ankündigungen veröffentlicht.", "Noch keine Themen veröffentlicht.",
		`href="/events"`, `href="/news"`, `href="/posts"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "<!doctype") || strings.Contains(body, `<html`) {
		t.Fatal("homepage body must not include the document layout")
	}
}

func TestHomeSectionsRenderTheirOwnContracts(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	post := HomePost{
		Title: `<script>alert(1)</script>`, Summary: `A <b>discussion</b>`,
		URL: "/posts/example", VoteURL: "/posts/example/vote",
		Date: "31. Oktober 2026", DateISO: "2026-10-31", Votes: 4, UserVote: 1,
	}
	for _, signedIn := range []bool{false, true} {
		name := "anonymous"
		if signedIn {
			name = "authenticated"
		}
		t.Run(name, func(t *testing.T) {
			page := Page{SignedIn: signedIn, CSRF: "csrf-token", Home: HomePage{Posts: []HomePost{post}}}
			var out strings.Builder
			if err := renderer.pages["landing"].ExecuteTemplate(&out, "home/forum", page.LandingContent().Posts); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			for _, want := range []string{`href="/posts/example"`, `datetime="2026-10-31"`, "&lt;script&gt;", "A &lt;b&gt;discussion&lt;/b&gt;"} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, "<script>") || strings.Contains(body, `<b>discussion</b>`) {
				t.Fatal("post content was not escaped")
			}
			if signedIn {
				for _, want := range []string{`action="/posts/example/vote"`, `name="csrf" value="csrf-token"`, `name="return" value="/#home-forum"`, `value="0"`, `aria-pressed="true"`} {
					if !strings.Contains(body, want) {
						t.Errorf("missing voting behavior %q", want)
					}
				}
			} else if strings.Contains(body, "<form") || strings.Contains(body, "csrf-token") || !strings.Contains(body, `href="/login"`) {
				t.Fatal("anonymous voting must offer login without exposing a form token")
			}
		})
	}
	t.Run("announcements", func(t *testing.T) {
		var out strings.Builder
		if err := renderer.pages["landing"].ExecuteTemplate(&out, "home/announcements", []HomePost{post}); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, `datetime="2026-10-31"`) || strings.Contains(body, "app-vote") || strings.Contains(body, "discussion") {
			t.Fatal("announcements must render escaped titles and dates without forum metadata")
		}
	})
}

func TestLandingLayoutUsesPreparedContent(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, signedIn := range []bool{false, true} {
		var out strings.Builder
		if err := renderer.Render(&out, "landing", false, Page{
			Title: "Startseite", Active: "home", SignedIn: signedIn, CSRF: "csrf-token",
			Organization: Organization{Name: "Testfachschaft", ShortName: "FS Test"},
			Home:         HomePage{Posts: []HomePost{{Title: "Testbeitrag", URL: "/posts/example", VoteURL: "/posts/example/vote"}}},
		}); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		for _, want := range []string{"<!doctype html>", "Deine FS Test", "Testbeitrag", `id="home-forum"`} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q", want)
			}
		}
	}
}
