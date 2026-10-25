package site

import (
	"errors"
	"net/http"
	"strings"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/model/organization"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) OrganizationSettings(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	id := r.PathValue("id")
	editor := !w.(*sessionResponse).session.CanAdmin()
	readOnly := editor && (id == "identity" || id == "legal")
	if readOnly && r.Method == http.MethodPost {
		c.pageError(w, "Du hast keine Berechtigung für diese Stammdaten.", http.StatusForbidden)
		return
	}
	if id == "" {
		a.View = "settings"
		roles := append(c.members.Roles("board"), c.members.Roles("departments")...)
		details := c.organization.Current()
		a.OrganizationSettings = view.OrganizationSettingsTasks(organizationView(details), mapRecords(roles, memberRoleView), details.Validate())
		c.adminRender(w, "Stammdaten", a, http.StatusOK)
		return
	}
	title := ""
	for _, section := range view.OrganizationSettingsSections {
		if section.ID == id {
			title = section.Title
		}
	}
	if title == "" {
		c.notFound(w, r)
		return
	}
	d := c.organization.Current()
	fields := map[string]string{}
	if r.Method == http.MethodPost {
		get := func(key string) string { return strings.TrimSpace(r.PostForm.Get(key)) }
		switch id {
		case "identity":
			d.Name, d.ShortName, d.FormalName, d.Institution = get("Name"), get("ShortName"), get("FormalName"), get("Institution")
		case "contact":
			d.ContactEmail, d.Street, d.PostalCode, d.City, d.Country, d.Office = get("ContactEmail"), get("Street"), get("PostalCode"), get("City"), get("Country"), get("Office")
			roles := append(c.members.Roles("board"), c.members.Roles("departments")...)
			resolve := func(value string) (string, bool) {
				if value == "" {
					return "", true
				}
				for _, role := range roles {
					if role.PublicID == value {
						return role.ID, true
					}
				}
				return "", false
			}
			var chairOK, publicityOK bool
			d.ChairRole, chairOK = resolve(get("ChairRole"))
			d.PublicityRole, publicityOK = resolve(get("PublicityRole"))
			if !chairOK || !publicityOK {
				c.notFound(w, r)
				return
			}
		case "legal":
			d.HostingName, d.HostingURL, d.PrivacyURL = get("HostingName"), get("HostingURL"), get("PrivacyURL")
			d.SupervisionName, d.SupervisionURL = get("SupervisionName"), get("SupervisionURL")
			d.PrivacyAuthorityName, d.PrivacyAuthorityURL = get("PrivacyAuthorityName"), get("PrivacyAuthorityURL")
			urls := r.PostForm["RegulationLinksURL"]
			for i := range d.RegulationLinks {
				d.RegulationLinks[i].URL = ""
				if i < len(urls) {
					d.RegulationLinks[i].URL = strings.TrimSpace(urls[i])
				}
			}
		case "links":
			d.SocialLinks = parseLinks(r, "SocialLinks")
			d.ElectionLinks = parseLinks(r, "ElectionLinks")
		}
		var err error
		fields, err = c.organization.Save(r.Context(), w.(*sessionResponse).session.UserID, d)
		if errors.Is(err, organization.ErrForbidden) {
			c.pageError(w, "Du hast keine Berechtigung für die Stammdaten.", http.StatusForbidden)
			return
		}
		if err != nil {
			c.serverError(w, err)
			return
		}
		if len(fields) == 0 {
			if id == "contact" || id == "links" {
				audit.Content(r.Context(), "content.settings_saved", title, "/admin/settings/"+id)
			}
			flash(w, "Die Änderungen wurden gespeichert.")
			http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
			return
		}
	}
	roles := append(c.members.Roles("board"), c.members.Roles("departments")...)
	a.View, a.Action = "form", r.URL.Path
	if readOnly {
		a.View = "settings-readonly"
		a.ReadOnly = true
	}
	a.Fields = view.OrganizationSettingsFields(id, organizationView(d), mapRecords(roles, memberRoleView))
	view.AttachErrors(a.Fields, fields)
	status := http.StatusOK
	if len(fields) > 0 {
		status = http.StatusUnprocessableEntity
	}
	c.adminRender(w, title, a, status)
}

func parseLinks(r *http.Request, key string) []organization.Link {
	titles, urls := r.PostForm[key+"Title"], r.PostForm[key+"URL"]
	var links []organization.Link
	for i := 0; i < max(len(titles), len(urls)); i++ {
		var link organization.Link
		if i < len(titles) {
			link.Title = strings.TrimSpace(titles[i])
		}
		if i < len(urls) {
			link.URL = strings.TrimSpace(urls[i])
		}
		if link.Title != "" || link.URL != "" {
			links = append(links, link)
		}
	}
	return links
}
