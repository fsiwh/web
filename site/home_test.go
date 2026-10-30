package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHomeFeaturedEventsIncludeRecentPast(t *testing.T) {
	c, db := testApplication(t)
	if _, err := db.Exec(`DELETE FROM calendar_events`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, event := range []struct {
		title string
		days  int
	}{
		{"Next event", 2}, {"Later event", 5},
		{"Recent event", -2}, {"Older event", -5}, {"Oldest event", -10},
	} {
		if _, err := db.Exec(`INSERT INTO calendar_events(id,title,body,user_id,event_date) VALUES(?,?,?,900,?)`, 800+i, event.title, "Event summary", now.AddDate(0, 0, event.days).Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		publicRef(t, db, "events", strconv.Itoa(800+i))
	}
	page, err := c.home(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Next event", "Later event", "Recent event", "Older event"}
	if len(page.Events) != len(want) {
		t.Fatalf("got %d featured events, want %d", len(page.Events), len(want))
	}
	for i, title := range want {
		if page.Events[i].Title != title {
			t.Fatalf("event %d: got %q, want %q", i, page.Events[i].Title, title)
		}
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Count(body, "app-featured-card--large") != 1 || strings.Count(body, `class="app-featured-card"`) != 3 {
		t.Fatalf("expected one lead and three smaller event cards, status %d", w.Code)
	}
	if strings.Contains(body, "Neues aus der Fachschaft") || strings.Contains(body, "Oldest event") {
		t.Fatal("homepage contains removed news section or an event beyond the four-item limit")
	}
}

func TestHomeLatestAnnouncements(t *testing.T) {
	c, db := testApplication(t)
	if _, err := db.Exec(`DELETE FROM discussion_posts`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		kind := "news"
		if i == 4 {
			kind = "discussion"
		}
		_, err := db.Exec(`INSERT INTO discussion_posts(id,title,body,user_id,type,created_at) VALUES(?,?,?,900,?,?)`, 1800+i, "Latest item "+strconv.Itoa(i), "Announcement body", kind, time.Now().UTC().Add(time.Duration(i)*time.Hour).Format(time.RFC3339))
		if err != nil {
			t.Fatal(err)
		}
		publicRef(t, db, "posts", strconv.Itoa(1800+i))
	}
	page, err := c.home(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Announcements) != 3 {
		t.Fatalf("got %d announcements, want 3", len(page.Announcements))
	}
	for i, post := range page.Announcements {
		if want := "Latest item " + strconv.Itoa(3-i); post.Title != want {
			t.Fatalf("announcement %d: got %q, want %q", i, post.Title, want)
		}
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	body := w.Body.String()
	start := strings.Index(body, `<section class="app-home-latest"`)
	if start < 0 {
		t.Fatal("missing latest announcements section")
	}
	section := body[start:]
	section = section[:strings.Index(section, "</section>")]
	if w.Code != http.StatusOK || strings.Count(section, "<time ") != 3 || !strings.Contains(section, `href="/news"`) || strings.Contains(section, "Latest item 0") || strings.Contains(section, "Latest item 4") {
		t.Fatal("latest announcements section must show only the three newest news posts and the news archive link")
	}
}
