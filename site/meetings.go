package site

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/meetings"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) EditPublication(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	owner := c.publicationOwner(w)
	id := r.PathValue("id")
	p := meetings.Publication{ID: id, Owner: owner, Date: r.URL.Query().Get("date")}
	p.Number, _ = strconv.Atoi(r.URL.Query().Get("number"))
	if id != "create" {
		internal, err := c.meetings.ResolvePublicID(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			c.notFound(w, r)
			return
		}
		if err != nil {
			c.serverError(w, err)
			return
		}
		var ok bool
		p, ok, err = c.meetings.GetWithError(internal)
		if err != nil {
			c.serverError(w, err)
			return
		}
		if !ok || p.Owner != owner || p.Published {
			c.notFound(w, r)
			return
		}
	}
	errors := map[string]string{}
	day, month, year := "", "", ""
	if date, err := time.Parse("2006-01-02", p.Date); err == nil {
		day, month, year = strconv.Itoa(date.Day()), strconv.Itoa(int(date.Month())), strconv.Itoa(date.Year())
	}
	number := ""
	if p.Number > 0 {
		number = strconv.Itoa(p.Number)
	}
	if r.Method == http.MethodPost {
		if id == "create" {
			p.ID = publicid.New()
		}
		day, month, year = r.PostForm.Get("day"), r.PostForm.Get("month"), r.PostForm.Get("year")
		d, _ := strconv.Atoi(day)
		m, _ := strconv.Atoi(month)
		y, _ := strconv.Atoi(year)
		p.Date = fmt.Sprintf("%04d-%02d-%02d", y, m, d)
		number = r.PostForm.Get("number")
		p.Number, _ = strconv.Atoi(number)
		p.Kind = r.PostForm.Get("kind")
		file, header, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			data, readErr := io.ReadAll(io.LimitReader(file, meetings.MaxPDFBytes+1))
			candidate := p
			candidate.Data = data
			if readErr != nil {
				errors["file"] = "Die Datei konnte nicht gelesen werden. Wähle sie erneut aus."
			} else {
				if msg := meetings.ValidatePublication(candidate)["file"]; msg != "" {
					errors["file"] = msg
				} else {
					p.Data, p.Filename = data, filepath.Base(header.Filename)
				}
			}
		} else {
			if err != http.ErrMissingFile {
				errors["file"] = "Die Datei konnte nicht gelesen werden. Wähle sie erneut aus."
			}
		}
		for key, message := range meetings.ValidatePublication(p) {
			if errors[key] == "" {
				errors[key] = message
			}
		}
		if err := c.meetings.Save(p); err != nil {
			a.Message = err.Error()
			if id == "create" {
				p.ID = "create"
				p.Data = nil
				if errors["file"] == "" {
					errors["file"] = "Die Datei wurde nicht gespeichert. Wähle sie erneut aus."
				}
			}
		} else {
			p.PublicID, err = c.meetings.PublicID(r.Context(), p.ID)
			if err != nil {
				c.serverError(w, err)
				return
			}
			if len(errors) == 0 {
				http.Redirect(w, r, urls.AdminMeeting(p.PublicID)+"/check", http.StatusSeeOther)
				return
			}
		}
	}
	a.View, a.Action, a.Publication = "form", r.URL.Path, ptr(publicationView(p))
	if p.ID != "create" {
		a.Action = urls.EditAdminMeeting(p.PublicID)
	}
	a.Fields = []view.AdminField{
		{ID: "date", Label: "Sitzungsdatum", Kind: "date", Day: day, Month: month, Year: year},
		view.Field("number", "Sitzungsnummer", number, "text"),
		{ID: "kind", Label: "Dokumentart", Kind: "radios", Options: []view.AdminOption{{Value: "Einladung", Label: "Einladung zur Sitzung", Selected: p.Kind == "Einladung"}, {Value: "Protokoll", Label: "Sitzungsprotokoll", Selected: p.Kind == "Protokoll"}}},
		{ID: "file", Label: "Datei", Kind: "file", Hint: "PDF, höchstens 8 MB."},
	}
	view.AttachErrors(a.Fields, errors)
	status := http.StatusOK
	if len(errors) != 0 || a.Message != "" {
		status = http.StatusUnprocessableEntity
	}
	c.adminRender(w, "Dokument hinzufügen", a, status)
}

func (c *Handler) ReviewPublication(w http.ResponseWriter, r *http.Request) {
	owner := c.publicationOwner(w)
	internal, err := c.meetings.ResolvePublicID(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		c.notFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	p, ok, err := c.meetings.GetWithError(internal)
	if err != nil {
		c.serverError(w, err)
		return
	}
	if !ok || p.Owner != owner {
		c.notFound(w, r)
		return
	}
	a := &view.AdminPage{Section: "meetings", SectionTitle: "Sitzungen und Protokolle", View: "review", Publication: ptr(publicationView(p)), Action: r.URL.Path}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if err := c.meetings.Publish(p.ID, owner); err != nil {
			a.Message = err.Error()
			status = http.StatusUnprocessableEntity
		} else {
			audit.Content(r.Context(), "content.meeting_published", p.Kind+" · "+p.Date, urls.MeetingsPath)
			flash(w, "Das Dokument wurde veröffentlicht.")
			http.Redirect(w, r, urls.AdminMeetingsPath, http.StatusSeeOther)
			return
		}
	}
	if p.Published {
		http.Redirect(w, r, urls.AdminMeetingsPath, http.StatusSeeOther)
		return
	}
	c.adminRender(w, "Veröffentlichung prüfen", a, status)
}

func (c *Handler) publicationOwner(w http.ResponseWriter) string {
	s := w.(*sessionResponse).session
	if s == nil || !s.Authenticated {
		return ""
	}

	if s.UserID <= 0 {
		return ""
	}
	return strconv.FormatInt(s.UserID, 10)

}

func (c *Handler) Meetings(w http.ResponseWriter, r *http.Request) {
	all, err := c.meetings.AllWithError()
	if err != nil {
		c.serverError(w, err)
		return
	}
	var years []int
	var next *meetings.Meeting
	today := time.Now().Format("2006-01-02")
	for _, meeting := range all {
		if len(years) == 0 || years[len(years)-1] != meeting.Year {
			years = append(years, meeting.Year)
		}
		if meeting.Date >= today && (next == nil || meeting.Date < next.Date) {
			copy := meeting
			next = &copy
		}
	}
	year := time.Now().Year()
	if len(years) > 0 {
		year = years[0]
	}
	if value := r.URL.Query().Get("year"); value != "" {
		var err error
		year, err = strconv.Atoi(value)
		if err != nil {
			c.notFound(w, r)
			return
		}
	}
	var records []meetings.Meeting
	for _, meeting := range all {
		if meeting.Year == year {
			records = append(records, meeting)
		}
	}
	if len(records) == 0 && len(all) > 0 || len(records) == 0 && r.URL.Query().Has("year") {
		c.notFound(w, r)
		return
	}
	var nextMeeting *view.Meeting
	if next != nil {
		nextMeeting = ptr(meetingView(*next))
	}
	c.render(w, "content", false, view.Page{
		Title: "Sitzungen und Protokolle", Active: "meetings", About: true,
		Meetings: view.MeetingsPage{Year: year, Years: years, Meetings: mapRecords(records, meetingView), Next: nextMeeting},
	})
}
