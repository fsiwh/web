package site

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/view"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/model/events"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/storage/disk"
)

func TestEventPublicationWorkflow(t *testing.T) {
	ctx := context.Background()
	path := testdb.Path(t)
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO users(id,email,name,password,active,verified,role) VALUES(1,'editor@council.example.invalid','Editor','unused',1,1,'editor'),(2,'member@council.example.invalid','Member','unused',1,1,'user')`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	storage, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	views, err := view.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	c := &Handler{views: views, identity: users.NewService(db, "http://example.test", users.DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}}), events: events.NewService(db, storage), posts: posts.NewService(db)}
	mux := http.NewServeMux()
	write := routePolicy{authenticated: true, csrf: true, bodyLimit: 8 << 20}
	mux.Handle("/events/create", c.withSession(write, http.HandlerFunc(c.EditEvent)))
	mux.Handle("/events/{event}/edit", c.withSession(write, http.HandlerFunc(c.EditEvent)))
	mux.Handle("/events/{event}/cover", c.withSession(routePolicy{}, http.HandlerFunc(c.CalendarCover)))
	mux.Handle("/events/{event}", c.withSession(routePolicy{}, http.HandlerFunc(c.Events)))
	mux.Handle("/events", c.withSession(routePolicy{}, http.HandlerFunc(c.Events)))
	mux.Handle("/posts/create", c.withSession(write, http.HandlerFunc(c.EditDiscussion)))
	mux.Handle("/news", c.withSession(routePolicy{}, http.HandlerFunc(c.News)))
	mux.Handle("/posts", c.withSession(routePolicy{}, http.HandlerFunc(c.Posts)))
	handler := mux
	staff, err := c.identity.CreateSession(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	member, err := c.identity.CreateSession(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, s *session, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
		t.Helper()
		var reader io.Reader
		if body != nil {
			reader = body
		}
		r := httptest.NewRequest(method, path, reader)
		if s != nil {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ID})
		}
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		s    *session
		code int
	}{{nil, 303}, {member, 403}, {staff, 200}} {
		w := request("GET", view.EventsURL+"/create", tc.s, nil, "")
		if w.Code != tc.code {
			t.Fatalf("editor access: %d %s", w.Code, w.Body.String())
		}
	}
	upload := func(csrf string) (*bytes.Buffer, string) {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for key, value := range map[string]string{"csrf": csrf, "title": "Sommergrillen", "body": "Gemeinsam im Innenhof grillen.", "date-day": "30", "date-month": "6", "date-year": "2027", "time": "17:00", "location": "Innenhof", "cover_alt": "Plakat zum Sommergrillen", "type": "discussion"} {
			if err := form.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		file, err := form.CreateFormFile("cover", "poster.png")
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 120, 200))); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		return &body, form.FormDataContentType()
	}
	body, kind := upload("invalid")
	if w := request("POST", view.EventsURL+"/create", staff, body, kind); w.Code != 403 {
		t.Fatal("event accepted without CSRF")
	}
	body, kind = upload(staff.CSRF)
	w := request("POST", view.EventsURL+"/create", staff, body, kind)
	if w.Code != 303 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	location := w.Header().Get("Location")
	event, err := c.events.Lookup(ctx, strings.TrimPrefix(location, "/events/"))
	if err != nil || event.EventDate != "2027-06-30T15:00:00Z" {
		t.Fatalf("split date did not preserve Berlin time: %+v %v", event, err)
	}
	w = request("GET", location+"/edit", staff, nil, "")
	for _, want := range []string{`name="date-day"`, `name="date-month"`, `name="date-year"`, `value="30"`, `value="06"`, `value="2027"`} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("edit date missing %s: %d", want, w.Code)
		}
	}
	for _, tc := range []struct{ day, month, year, clock, errorID string }{
		{"30", "", "2027", "17:00", "date-error"},
		{"31", "2", "2027", "17:00", "date-error"},
		{"", "", "", "17:00", "date-error"},
		{"30", "6", "2027", "25:00", "time-error"},
	} {
		form := url.Values{"csrf": {staff.CSRF}, "title": {"Terminprüfung"}, "body": {"Ein ausreichender Veranstaltungstext."}, "date-day": {tc.day}, "date-month": {tc.month}, "date-year": {tc.year}, "time": {tc.clock}}
		w = request("POST", "/events/create", staff, bytes.NewBufferString(form.Encode()), "application/x-www-form-urlencoded")
		if w.Code != 422 || !strings.Contains(w.Body.String(), tc.errorID) || strings.Contains(w.Body.String(), "date-hintdate-error") || strings.Contains(w.Body.String(), "time-hinttime-error") {
			t.Fatalf("invalid schedule: %d %s", w.Code, w.Body.String())
		}
		for _, value := range []string{tc.day, tc.month, tc.year, tc.clock} {
			if !strings.Contains(w.Body.String(), `value="`+value+`"`) {
				t.Fatal("schedule input lost after validation error")
			}
		}
	}
	w = request("GET", location, nil, nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Plakat zum Sommergrillen") || !strings.Contains(w.Body.String(), "Innenhof") || strings.Contains(w.Body.String(), "Veranstaltung bearbeiten") {
		t.Fatalf("public detail: %d %s", w.Code, w.Body.String())
	}
	if w = request("GET", location+"/cover", nil, nil, ""); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("public cover unavailable")
	}
	if w = request("GET", location+"/edit", member, nil, ""); w.Code != 403 {
		t.Fatal("member could edit event")
	}
	if w = request("GET", view.EventsURL, nil, nil, ""); !strings.Contains(w.Body.String(), location) {
		t.Fatal("event absent from list")
	}
	if w = request("GET", "/posts", nil, nil, ""); strings.Contains(w.Body.String(), "Sommergrillen") {
		t.Fatal("event leaked into forum")
	}
	if w = request("GET", "/posts/create?type=news", member, nil, ""); w.Code != 403 {
		t.Fatal("member could publish news")
	}
	if w = request("GET", "/posts/create?type=news", staff, nil, ""); w.Code != 200 || strings.Contains(w.Body.String(), `name="type"`) {
		t.Fatal("dedicated news editor unavailable")
	}
}
