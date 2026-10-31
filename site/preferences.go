package site

import (
	"errors"
	"net/http"
	"strings"

	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) AccountPreferences(w http.ResponseWriter, r *http.Request) {
	s := w.(*sessionResponse).session
	p, err := c.identity.Preferences(r.Context(), s.UserID)
	if err != nil {
		c.serverError(w, err)
		return
	}
	page := view.Page{Title: "Kontoeinstellungen", Active: "account", Preferences: view.PreferencesPage{
		Name: p.Name, Email: p.Email, PendingEmail: p.PendingEmail, Section: r.PathValue("section"),
	}}
	form := &page.Preferences
	current := view.AccountField{ID: "current-password", Label: "Aktuelles Passwort", Type: "password", Autocomplete: "current-password"}
	switch form.Section {
	case "name":
		page.Title, form.Action = "Name ändern", urls.AccountNamePath
		form.Fields = []view.AccountField{{ID: "name", Label: "Name", Type: "text", Autocomplete: "name", Value: p.Name}}
	case "email":
		page.Title, form.Action = "E-Mail-Adresse ändern", urls.AccountEmailPath
		email := p.Email
		if p.PendingEmail != "" {
			email = p.PendingEmail
		}
		form.Fields = []view.AccountField{{ID: "email", Label: "Neue E-Mail-Adresse", Type: "email", Autocomplete: "email", Value: email, Hint: "Nur Adressen mit der Endung"}, current}
	case "password":
		page.Title, form.Action = "Passwort ändern", urls.AccountPasswordPath
		form.Fields = []view.AccountField{current, {ID: "password", Label: "Neues Passwort", Type: "password", Autocomplete: "new-password", Hint: "Verwende mindestens 8 Zeichen"}}
	case "notifications":
		page.Title = "Welche Benachrichtigungen möchtest du erhalten?"
		form.ReviewerNotificationsEnabled, form.ReviewerNotifications, err = c.exams.SubmissionMailPreference(r.Context(), s.UserID)
		if err != nil {
			c.serverError(w, err)
			return
		}
	case "":
		form.Success = takeFlash(w)
		filter, err := c.exams.Settings(r.Context(), s.UserID)
		if err != nil {
			c.serverError(w, err)
			return
		}
		catalog, err := c.exams.Catalog(r.Context())
		if err != nil {
			c.serverError(w, err)
			return
		}
		var programs, degrees []string
		for _, program := range filter.Programs {
			programs = append(programs, catalog.ProgramLabel(program))
		}
		for _, degree := range filter.Degrees {
			degrees = append(degrees, exams.DegreeLabel(degree))
		}
		form.StudyPrograms = strings.Join(programs, ", ")
		form.StudyDegrees = strings.Join(degrees, ", ")
		form.StudyVersions = strings.Join(filter.Versions, ", ")
	default:
		c.notFound(w, r)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		for i := range form.Fields {
			f := &form.Fields[i]
			switch f.ID {
			case "name":
				f.Value = strings.TrimSpace(r.PostForm.Get(f.ID))
				f.Error = users.NameError(f.Value)
			case "email":
				f.Value = strings.TrimSpace(r.PostForm.Get(f.ID))
				f.Error = c.identity.EmailError(f.Value)
			case "current-password":
				if r.PostForm.Get(f.ID) == "" {
					f.Error = "Gib dein aktuelles Passwort ein."
				}
			case "password":
				f.Error = users.PasswordError(r.PostForm.Get(f.ID))
			}
		}
		if !form.HasErrors() {
			switch form.Section {
			case "name":
				err = c.identity.ChangeName(r.Context(), s.UserID, form.Fields[0].Value)
			case "email":
				err = c.identity.RequestEmailChange(r.Context(), s.UserID, form.Fields[0].Value, r.PostForm.Get("current-password"))
			case "password":
				err = c.identity.ChangePassword(r.Context(), s.UserID, s.ID, r.PostForm.Get("current-password"), r.PostForm.Get("password"))
			}
			if err == nil {
				message := "Dein Name wurde geändert."
				if form.Section == "email" {
					message = "Die E-Mail zur Bestätigung wurde zum Versand vorgemerkt."
				} else if form.Section == "password" {
					message = "Dein Passwort wurde geändert. Du wurdest auf allen anderen Geräten abgemeldet."
				}
				flash(w, message)
				http.Redirect(w, r, urls.AccountPath, http.StatusSeeOther)
				return
			}
			field := ""
			if errors.Is(err, users.ErrCurrentPassword) {
				field = "current-password"
			} else if errors.Is(err, users.ErrEmailExists) || errors.Is(err, users.ErrEmailUnchanged) {
				field = "email"
			} else {
				c.serverError(w, err)
				return
			}
			for i := range form.Fields {
				if form.Fields[i].ID == field {
					form.Fields[i].Error = err.Error()
				}
			}
		}
		status = http.StatusUnprocessableEntity
	}
	c.renderStatus(w, "preferences", false, page, status)
}
