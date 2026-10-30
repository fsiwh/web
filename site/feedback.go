package site

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/fachschaftinformatik/web/view"
)

type PageFeedbackSender interface {
	SendPageFeedback(path, value, expected, actual string) error
}

func (c *Handler) PageFeedback(w http.ResponseWriter, r *http.Request) {
	f := view.FeedbackPage{
		Path: r.FormValue("path"), Value: r.FormValue("value"),
		Expected: strings.TrimSpace(r.FormValue("expected")), Actual: strings.TrimSpace(r.FormValue("actual")),
	}
	// Keep only a local path: query strings may contain private search or account data.
	u, err := url.Parse(f.Path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		f.Path = "/"
	} else {
		f.Path = u.EscapedPath()
	}
	if f.Value != "yes" && f.Value != "no" && f.Value != "problem" {
		c.pageError(w, "Wähle eine Antwort aus.", http.StatusBadRequest)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost && r.PostForm.Get("cancel") == "1" {
		http.Redirect(w, r, f.Path, http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost {
		if utf8.RuneCountInString(f.Expected) > 2000 || utf8.RuneCountInString(f.Actual) > 2000 {
			f.Error = "Deine Angaben dürfen jeweils höchstens 2000 Zeichen enthalten."
		} else if f.Value == "problem" && (f.Expected == "" || f.Actual == "") {
			f.Error = "Beschreibe, was du tun wolltest und was passiert ist."
		}
		if f.Error != "" {
			status = http.StatusUnprocessableEntity
		} else {
			if c.feedbackMail == nil {
				err = fmt.Errorf("page feedback mail is not configured")
			} else {
				err = c.feedbackMail.SendPageFeedback(f.Path, f.Value, f.Expected, f.Actual)
			}
			if err != nil {
				c.serverError(w, err)
				return
			}
			f.Sent = true
		}
	}
	c.renderStatus(w, "feedback", false, view.Page{Title: "Rückmeldung zu dieser Seite", Active: "feedback", Feedback: f}, status)
}
