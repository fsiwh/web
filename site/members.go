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
	if r.PathValue("id") == "create" {
		c.InviteUser(w, r)
		return
	}
	if err := c.members.Refresh(r.Context()); err != nil {
		c.serverError(w, err)
		return
	}
	var p members.Member
	found := false
	for _, u := range c.members.Members() {
		if u.PublicID == r.PathValue("id") {
			p = u
			found = true
			break
		}
	}
	if !found {
		c.notFound(w, r)
		return
	}
	directory, err := c.identity.Directory(r.Context())
	if err != nil {
		c.serverError(w, err)
		return
	}
	account, ok := directory.Get(p.PublicID)
	if !ok {
		c.notFound(w, r)
		return
	}
	p.Name = account.Name
	fields := map[string]string{}
	if r.Method == http.MethodPost {
		p.Name, p.Email = r.PostForm.Get("name"), r.PostForm.Get("public_email")
		fields = c.members.SaveMember(p)
		if len(fields) == 0 {
			flash(w, "Die Änderungen wurden gespeichert.")
			http.Redirect(w, r, urls.AdminUser(p.PublicID), http.StatusSeeOther)
			return
		}
		if message := fields["email"]; message != "" {
			fields["public_email"] = message
		}
	}
	a.View, a.Action = "form", "/admin/users/"+p.PublicID+"/edit"
	a.Member = ptr(memberView(p))
	a.Fields = []view.AdminField{view.Field("name", "Name", p.Name, "text"), view.Field("public_email", "Öffentliche E-Mail-Adresse (optional)", p.Email, "email")}
	a.Fields[1].Hint = "Die Anmeldeadresse bleibt privat. Funktionsadressen haben auf der Teamseite Vorrang."
	view.AttachErrors(a.Fields, fields)
	status := http.StatusOK
	if len(fields) > 0 {
		status = http.StatusUnprocessableEntity
	}
	c.adminRender(w, "Benutzerkonto bearbeiten", a, status)
}

func (c *Handler) EditRole(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	id := r.PathValue("id")
	role := members.MemberRole{ID: id, OrganizationID: 1}
	found := id == "create"
	for _, kind := range []string{"board", "departments", "project"} {
		for _, f := range c.members.Roles(kind) {
			if f.PublicID == id {
				role = f
				found = true
			}
		}
	}
	if !found {
		c.notFound(w, r)
		return
	}
	fields := map[string]string{}
	if r.Method == http.MethodPost {
		role.Title, role.Email, role.Group = r.PostForm.Get("title"), r.PostForm.Get("email"), r.PostForm.Get("group")
		role.MemberIDs = nil
		for _, value := range r.PostForm["members"] {
			matched := false
			for _, u := range c.members.AssignableMembers() {
				if u.PublicID == value {
					role.MemberIDs = append(role.MemberIDs, u.ID)
					matched = true
					break
				}
			}
			if !matched {
				c.notFound(w, r)
				return
			}
		}
		fields = c.members.SaveRole(role)
		if len(fields) == 0 {
			audit.Content(r.Context(), "content.function_saved", role.Title, urls.AdminRolesPath)
			flash(w, "Die Änderungen wurden gespeichert.")
			http.Redirect(w, r, urls.AdminRolesPath, http.StatusSeeOther)
			return
		}
	}
	a.View, a.Action = "form", r.URL.Path
	a.Fields = []view.AdminField{view.Field("title", "Bezeichnung", role.Title, "text"), view.Field("group", "Art", role.Group, "radios"), view.Field("email", "Öffentliche E-Mail-Adresse (optional)", role.Email, "email"), view.Field("members", "Benutzerkonten", "", "checkboxes")}
	a.Fields[1].Options = []view.AdminOption{{Value: "board", Label: "Vorstand", Selected: role.Group == "board"}, {Value: "departments", Label: "Referat", Selected: role.Group == "departments"}, {Value: "project", Label: "Projektstelle", Selected: role.Group == "project"}}
	for _, u := range c.members.AssignableMembers() {
		a.Fields[3].Options = append(a.Fields[3].Options, view.AdminOption{Value: u.PublicID, Label: u.Name, Selected: slices.Contains(role.MemberIDs, u.ID)})
	}
	view.AttachErrors(a.Fields, fields)
	status := http.StatusOK
	if len(fields) > 0 {
		status = http.StatusUnprocessableEntity
	}
	title := "Funktion bearbeiten"
	if id == "create" {
		title = "Funktion hinzufügen"
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

func (c *Handler) EditPublicUser(w http.ResponseWriter, r *http.Request) {
	if err := c.members.Refresh(r.Context()); err != nil {
		c.serverError(w, err)
		return
	}
	var p members.Member
	found := false
	for _, u := range c.members.AssignableMembers() {
		if u.PublicID == r.PathValue("id") {
			p = u
			found = true
			break
		}
	}
	if !found {
		c.notFound(w, r)
		return
	}
	fields := map[string]string{}
	if r.Method == http.MethodPost {
		p.Name = r.PostForm.Get("name")
		var err error
		fields, err = c.members.SavePublicName(r.Context(), w.(*sessionResponse).session.UserID, p.ID, p.Name)
		if err != nil {
			c.serverError(w, err)
			return
		}
		if len(fields) == 0 {
			audit.Content(r.Context(), "content.member_saved", p.Name, urls.AdminRolesPath)
			flash(w, "Die Änderungen wurden gespeichert.")
			http.Redirect(w, r, urls.AdminRolesPath, http.StatusSeeOther)
			return
		}
	}
	a := &view.AdminPage{Section: "roles", SectionTitle: "Funktionen", View: "form", Action: r.URL.Path, ReviewerOnly: !w.(*sessionResponse).session.CanAdmin(), Fields: []view.AdminField{view.Field("name", "Öffentlicher Name", p.Name, "text")}}
	view.AttachErrors(a.Fields, fields)
	status := http.StatusOK
	if len(fields) > 0 {
		status = http.StatusUnprocessableEntity
	}
	c.adminRender(w, "Öffentlichen Namen bearbeiten", a, status)
}
