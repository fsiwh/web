package site

import (
	"net/http"
	"slices"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/members"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) EditMember(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	id := r.PathValue("id")
	p := members.Member{ID: id}
	found := id == "create"
	for _, person := range c.members.Members() {
		if person.PublicID == id {
			p = person
			found = true
		}
	}
	if !found {
		c.notFound(w, r)
		return
	}
	title := "Mitglied bearbeiten"
	if w.(*sessionResponse).session.CanAdmin() && (id == "create" || p.UserID == 0) {
		title = "Mitglied einladen"
		a.Inviting = true
	}
	accountEmail := ""
	errors := map[string]string{}
	if r.Method == http.MethodPost {
		p.Name = r.PostForm.Get("name")
		if a.Inviting {
			accountEmail = r.PostForm.Get("email")

			var err error
			errors, err = c.members.InviteMember(r.Context(), c.identity, p.ID, p.Name, accountEmail)
			if err != nil {
				c.serverError(w, err)
				return
			}
			if len(errors) == 0 {
				flash(w, "Das Mitglied wurde hinzugefügt.")
				http.Redirect(w, r, urls.AdminMembersPath, http.StatusSeeOther)
				return
			}
		} else {

			var err error
			errors, err = c.members.SavePublicName(r.Context(), w.(*sessionResponse).session.UserID, p.ID, p.Name)
			if err != nil {
				c.serverError(w, err)
				return
			}
		}
		if len(errors) == 0 {
			audit.Content(r.Context(), "content.member_saved", p.Name, urls.AdminMembersPath)
			flash(w, "Die Änderungen wurden gespeichert.")
			http.Redirect(w, r, urls.AdminMembersPath, http.StatusSeeOther)
			return
		}
	}
	a.View, a.Action = "form", r.URL.Path
	if id != "create" {
		a.Action = urls.EditAdminMember(id)
	}
	if id != "create" {
		if w.(*sessionResponse).session.CanAdmin() {
			a.Member = ptr(memberView(p))
		}
	}
	a.Fields = []view.AdminField{view.Field("name", "Name", p.Name, "text")}
	if a.Inviting {
		a.Fields = append(a.Fields, view.Field("email", "E-Mail-Adresse des Benutzerkontos", accountEmail, "email"))
		a.Fields[1].Hint = "Besteht bereits ein Benutzerkonto, wird es mit dem Mitglied verknüpft. Andernfalls wird ein Konto angelegt und eine Einladung gesendet."
	} else {

		a.Fields[0].Hint = "Der öffentliche Name auf der Teamseite."
	}

	view.AttachErrors(a.Fields, errors)
	status := http.StatusOK
	if len(errors) != 0 {
		status = http.StatusUnprocessableEntity
	}
	c.adminRender(w, title, a, status)
}

func (c *Handler) EditRole(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	id := r.PathValue("id")
	role := members.MemberRole{ID: id}
	found := id == "create"
	for _, kind := range []string{"board", "departments", "project"} {
		for _, existing := range c.members.Roles(kind) {
			if existing.PublicID == id {
				role = existing
				found = true
			}
		}
	}
	if !found {
		c.notFound(w, r)
		return
	}
	title := "Funktion bearbeiten"
	if id == "create" {
		title = "Funktion hinzufügen"
	}
	errors := map[string]string{}
	if r.Method == http.MethodPost {
		role.Title, role.Email = r.PostForm.Get("title"), r.PostForm.Get("email")
		role.MemberIDs = nil
		for _, value := range r.PostForm["members"] {
			found := false
			for _, member := range c.members.Members() {
				if member.PublicID == value {
					role.MemberIDs = append(role.MemberIDs, member.ID)
					found = true
					break
				}
			}
			if !found {
				c.notFound(w, r)
				return
			}
		}
		if value := r.PostForm.Get("group"); value != "" {
			role.Group = value
		}
		errors = c.members.SaveRole(role)
		if len(errors) == 0 {
			audit.Content(r.Context(), "content.function_saved", role.Title, urls.AdminRolesPath)
			flash(w, "Die Änderungen wurden gespeichert.")
			http.Redirect(w, r, urls.AdminRolesPath, http.StatusSeeOther)
			return
		}
	}
	a.View, a.Action = "form", r.URL.Path
	a.Fields = []view.AdminField{view.Field("title", "Bezeichnung", role.Title, "text"), view.Field("group", "Art", role.Group, "radios"), view.Field("email", "Öffentliche E-Mail-Adresse (optional)", role.Email, "email"), view.Field("members", "Mitglieder", "", "checkboxes")}
	a.Fields[1].Options = []view.AdminOption{{Value: "board", Label: "Vorstand", Selected: role.Group == "board"}, {Value: "departments", Label: "Referat", Selected: role.Group == "departments"}, {Value: "project", Label: "Projektstelle", Selected: role.Group == "project"}}
	for _, p := range c.members.Members() {
		a.Fields[3].Options = append(a.Fields[3].Options, view.AdminOption{Value: p.PublicID, Label: p.Name, Selected: slices.Contains(role.MemberIDs, p.ID)})
	}
	view.AttachErrors(a.Fields, errors)
	status := http.StatusOK
	if len(errors) != 0 {
		status = http.StatusUnprocessableEntity
	}
	c.adminRender(w, title, a, status)
}

func (c *Handler) Team(w http.ResponseWriter, r *http.Request) {
	if err := c.members.Refresh(r.Context()); err != nil {
		c.serverError(w, err)
		return
	}
	c.render(w, "team", false, view.Page{Title: "Mitglieder", Active: "team", About: true, Team: mapRecords(c.members.Groups(), teamGroupView)})
}
