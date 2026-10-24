package site

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) Admin(w http.ResponseWriter, r *http.Request) {
	if err := c.members.Refresh(r.Context()); err != nil {
		c.serverError(w, err)
		return
	}
	section, id := r.PathValue("section"), r.PathValue("id")
	a := &view.AdminPage{Section: section, View: "index", Success: takeFlash(w)}
	if section == "" {
		c.adminRender(w, "Verwaltung", a, http.StatusOK)
		return
	}
	for _, link := range view.AdminLinks {
		if link.ID == section {
			a.SectionTitle = link.Label
		}
	}
	if a.SectionTitle == "" {
		c.notFound(w, r)
		return
	}
	switch section {
	case "audit":
		c.AuditEvents(w, r, a)
		return
	case "settings":
		c.OrganizationSettings(w, r, a)
		return
	case "submissions":
		submissions, err := c.exams.Submissions(r.Context(), w.(*sessionResponse).session.UserID, true)
		if err != nil {
			c.examsWriteError(w, r, err)
			return
		}
		a.View, a.Submissions = "submissions", mapRecords(submissions, submissionView)
	case "users":
		directory, err := c.identity.Directory(r.Context())
		if err != nil {
			c.serverError(w, err)
			return
		}
		if id != "" {
			user, ok := directory.Get(id)
			if !ok {
				c.notFound(w, r)
				return
			}
			a.View, a.User = "user", ptr(userView(user))
			for _, member := range c.members.Members() {
				if member.UserID != 0 && member.UserID == user.ID {
					a.Member = ptr(memberView(member))
					break
				}
			}
			c.adminRender(w, user.Name, a, http.StatusOK)
			return
		}
		a.View, a.Query = "users", strings.TrimSpace(r.URL.Query().Get("search"))
		switch role := r.URL.Query().Get("role"); role {
		case "admin", "editor", "user":
			a.UserRoles = []string{role}
		}
		switch status := r.URL.Query().Get("status"); status {
		case "active", "inactive", "verified", "unverified":
			a.UserStatus = status
		}
		a.Page = requestedPage(r)
		page, ok := directory.Filter(users.DirectoryFilter{Query: a.Query, Roles: a.UserRoles, Status: a.UserStatus}, a.Page)
		if !c.validPageRange(w, r, page.Total, users.PerPage) {
			return
		}
		if !ok {
			c.notFound(w, r)
			return
		}
		a.Users, a.Total, a.Pages = mapRecords(page.Users, userView), page.Total, page.Pages
		if a.Page > 1 {
			a.PreviousURL = a.UserPageURL(a.Page - 1)
		}
		if a.Page < a.Pages {
			a.NextURL = a.UserPageURL(a.Page + 1)
		}
	case "members", "roles":
		if id != "" {
			if section == "roles" {
				c.EditRole(w, r, a)
			} else {
				c.EditMember(w, r, a)
			}
			return
		}
		a.View = "members"
		if section == "roles" {
			a.View = "roles"
		}
		members := c.members.Members()
		roles := append(c.members.Roles("board"), c.members.Roles("departments")...)
		roles = append(roles, c.members.Roles("project")...)
		for _, member := range members {
			row := view.AdminMemberRow{Member: memberView(member)}
			for _, role := range roles {
				if slices.Contains(role.MemberIDs, member.ID) {
					row.Roles = append(row.Roles, role.Title+" ("+view.MemberRoleKind(role.Group)+")")
				}
			}
			a.Members = append(a.Members, row)
		}
		for _, role := range roles {
			var names []string
			for _, member := range members {
				if slices.Contains(role.MemberIDs, member.ID) {
					names = append(names, member.Name)
				}
			}
			a.Roles = append(a.Roles, view.AdminRoleRow{Title: role.Title, Kind: view.MemberRoleKind(role.Group), Members: names, URL: urls.EditAdminRole(role.PublicID)})
		}
	case "meetings":
		if id != "" {
			c.EditPublication(w, r, a)
			return
		}
		a.View = "meetings"
		records, err := c.meetings.AllWithError()
		if err != nil {
			c.serverError(w, err)
			return
		}
		a.Meetings = mapRecords(records, meetingView)
	default:
		c.notFound(w, r)
		return
	}
	c.adminRender(w, a.SectionTitle, a, http.StatusOK)
}

// ResetUserPassword confirms the destination before queuing a reset email.
func (c *Handler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	directory, err := c.identity.Directory(r.Context())
	if err != nil {
		c.serverError(w, err)
		return
	}
	user, ok := directory.Get(r.PathValue("id"))
	if !ok {
		c.notFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if err := c.identity.RequestReset(r.Context(), user.Email); err != nil {
			c.serverError(w, err)
			return
		}
		flash(w, "Die E-Mail zum Zurücksetzen des Passworts wurde zum Versand vorgemerkt.")
		http.Redirect(w, r, urls.AdminUser(user.PublicID), http.StatusSeeOther)
		return
	}
	c.adminRender(w, "Passwort zurücksetzen", &view.AdminPage{Section: "users", SectionTitle: "Benutzerkonten", View: "reset", User: ptr(userView(user))}, http.StatusOK)
}

func (c *Handler) PublicProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := c.identity.PublicProfile(r.Context(), r.PathValue("id"))
	if errors.Is(err, users.ErrProfileNotFound) {
		c.notFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	records, total, err := c.posts.FindPage(r.Context(), posts.Filter{AuthorID: profile.ID}, requestedPage(r), 20, postsUser(w))
	if err != nil {
		c.serverError(w, err)
		return
	}
	if !c.validPageRange(w, r, total, 20) {
		return
	}
	pagination := listPagination(r, total, 20)
	c.render(w, "profile", false, view.Page{Title: profile.Name, CanonicalURL: pagination.PageURL(pagination.Page), Active: "posts", Community: true, Posts: view.PostsPage{Posts: mapRecords(records, postView), Pagination: pagination}})
}
