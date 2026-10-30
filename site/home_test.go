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
