package site

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

type feedbackCapture struct {
	path, value, expected, actual string
	calls                         int
	err                           error
}

func (s *feedbackCapture) SendPageFeedback(path, value, expected, actual string) error {
	s.calls++
	s.path, s.value, s.expected, s.actual = path, value, expected, actual
	return s.err
}

func TestPageFeedback(t *testing.T) {
	c, _ := testApplication(t)
	mail := &feedbackCapture{}
	c.feedbackMail = mail
	get := httptest.NewRecorder()
	c.ServeHTTP(get, httptest.NewRequest("GET", "/feedback?value=problem&path=%2Fteam%3Fprivate%3Dsecret", nil))
	if get.Code != 200 {
		t.Fatalf("form: %d %s", get.Code, get.Body.String())
	}
	token := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
	if len(token) != 2 {
		t.Fatal("missing CSRF token")
	}
	var cookie *http.Cookie
	for _, candidate := range get.Result().Cookies() {
		if candidate.Name == sessionCookie {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("missing session cookie")
	}
	post := func(values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/feedback", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		return w
	}
	values := url.Values{"csrf": {"invalid"}, "value": {"yes"}, "path": {"/team?private=secret"}}
	if w := post(values); w.Code != 403 || mail.calls != 0 {
		t.Fatalf("CSRF: %d, calls %d", w.Code, mail.calls)
	}
	values.Set("csrf", token[1])
	if w := post(values); w.Code != 200 || !strings.Contains(w.Body.String(), "Vielen Dank für deine Rückmeldung.") {
		t.Fatalf("vote: %d %s", w.Code, w.Body.String())
	}
	if mail.calls != 1 || mail.path != "/team" || mail.value != "yes" {
		t.Fatalf("vote delivery: %+v", mail)
	}
	values.Set("value", "no")
	if w := post(values); w.Code != 200 || mail.value != "no" || !strings.Contains(w.Body.String(), "Hilf uns,") || !strings.Contains(w.Body.String(), `target="_blank"`) || strings.Contains(w.Body.String(), "Vielen Dank für deine Rückmeldung.") {
		t.Fatalf("negative vote: %d", w.Code)
	}
	values.Set("cancel", "1")
	if w := post(values); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/team" || mail.calls != 2 {
		t.Fatalf("cancel: %d, calls %d", w.Code, mail.calls)
	}
	values.Del("cancel")
	values.Set("value", "problem")
	if w := post(values); w.Code != 422 || mail.calls != 2 {
		t.Fatalf("empty report: %d, calls %d", w.Code, mail.calls)
	}
	values.Set("expected", "Find an event")
	values.Set("actual", "The link is broken")
	if w := post(values); w.Code != 200 || mail.expected != "Find an event" || mail.actual != "The link is broken" {
		t.Fatalf("report: %d %+v", w.Code, mail)
	}
	values.Set("actual", strings.Repeat("x", 2001))
	if w := post(values); w.Code != 422 || mail.calls != 3 {
		t.Fatalf("long report: %d", w.Code)
	}
	values.Set("actual", "Broken link")
	values.Set("path", "https://example.com/evil")
	mail.err = errors.New("SMTP unavailable")
	if w := post(values); w.Code != 500 || strings.Contains(w.Body.String(), "Vielen Dank für deine Rückmeldung.") || mail.path != "/" {
		t.Fatalf("mail failure: %d", w.Code)
	}
}
