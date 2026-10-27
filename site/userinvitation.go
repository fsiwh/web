package site

import (
	"net/http"
	"slices"

	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/members"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) InviteUser(w http.ResponseWriter, r *http.Request) {
	if err := c.members.Refresh(r.Context()); err != nil {
		c.serverError(w, err)
		return
	}
	var functions []members.MemberRole
	for _, kind := range []string{"board", "departments", "project"} {
		for _, f := range c.members.Roles(kind) {
			// FSI is the site's organization (seeded with ID 1).
			if f.OrganizationID == 1 {
				functions = append(functions, f)
			}
		}
	}
	state := r.PostForm
	step := state.Get("step")
	if step == "" {
		step = "access"
	}
	role, kind := state.Get("role"), state.Get("kind")
	assigned := slices.Clone(state["assigned"])
	if role == "admin" {
		assigned = nil
		kind = ""
	}
	fields := map[string]string{}
	validRole := role == "editor" || role == "admin"
	validKind := kind == "board" || kind == "departments" || kind == "project"
	var available []members.MemberRole
	for _, f := range functions {
		if f.Group == kind {
			available = append(available, f)
		}
	}
	validAssignments := len(assigned) > 0
	for _, id := range assigned {
		if !slices.ContainsFunc(functions, func(f members.MemberRole) bool { return f.PublicID == id }) {
			validAssignments = false
		}
	}
	if r.Method == http.MethodPost {
		if remove := state.Get("remove"); step == "assignments" && remove != "" {
			assigned = slices.DeleteFunc(assigned, func(id string) bool { return id == remove })
			if len(assigned) == 0 {
				step = "kind"
			}
		} else if step == "assignments" && state.Get("add") == "true" {
			step = "kind"
			kind = ""
		} else {
			switch step {
			case "access":
				if !validRole {
					fields["role"] = "Wähle eine Zugangsart aus."
				} else if role == "admin" {
					step = "details"
				} else {
					step = "kind"
				}
			case "kind":
				if !validKind {
					fields["kind"] = "Wähle eine Art aus."
				} else if len(available) == 0 {
					fields["kind"] = "Für diese Art sind noch keine Funktionen angelegt."
				} else {
					step = "functions"
				}
			case "functions":
				selected := state["functions"]
				if len(selected) == 0 {
					fields["functions"] = "Wähle mindestens eine Funktion aus."
				}
				for _, id := range selected {
					if !slices.ContainsFunc(available, func(f members.MemberRole) bool { return f.PublicID == id }) {
						fields["functions"] = "Wähle Funktionen der gewählten Art aus."
					}
				}
				if len(fields) == 0 {
					for _, id := range selected {
						if !slices.Contains(assigned, id) {
							assigned = append(assigned, id)
						}
					}
					step = "assignments"
				}
			case "assignments":
				if !validAssignments {
					step = "kind"
				} else {
					step = "details"
				}
			case "details":
				if role == "editor" && !validAssignments {
					step = "kind"
					break
				}
				if validRole {
					var err error
					fields, err = c.members.InviteAccount(r.Context(), c.identity, state.Get("name"), state.Get("email"), role, assigned)
					if err != nil {
						c.serverError(w, err)
						return
					}
					if len(fields) == 0 {
						flash(w, "Das Benutzerkonto wurde eingeladen.")
						http.Redirect(w, r, urls.AdminUsersPath, http.StatusSeeOther)
						return
					}
				}
			default:
				step = "access"
			}
		}
	}
	// Validate prerequisites even when a later step is submitted directly.
	if step != "access" && !validRole {
		step = "access"
		fields = map[string]string{"role": "Wähle eine Zugangsart aus."}
	}
	if role == "admin" && step != "access" {
		step = "details"
	}
	if step == "functions" && !validKind {
		step = "kind"
	}
	a := &view.AdminPage{Section: "users", SectionTitle: "Benutzerkonten", View: "form", Action: "/admin/users/create", Inviting: true, InviteStep: step, SubmitLabel: "Weiter"}
	title := "Zugangsart auswählen"
	switch step {
	case "access":
		f := view.Field("role", "Zugangsart", role, "radios")
		f.Options = []view.AdminOption{{Value: "editor", Label: "Organisationsbezogener Zugang", Selected: role == "editor"}, {Value: "admin", Label: "Systemadministration", Selected: role == "admin"}}
		a.Fields = []view.AdminField{f}
	case "kind":
		title = "Art auswählen"
		f := view.Field("kind", "Art", kind, "radios")
		for _, k := range []string{"board", "departments", "project"} {
			f.Options = append(f.Options, view.AdminOption{Value: k, Label: view.MemberRoleKind(k), Selected: k == kind})
		}
		a.Fields = []view.AdminField{f}
	case "functions":
		title = "Funktionen auswählen"
		a.InviteContext = view.MemberRoleKind(kind)
		f := view.Field("functions", "Funktionen", "", "checkboxes")
		for _, item := range available {
			f.Options = append(f.Options, view.AdminOption{Value: item.PublicID, Label: item.Title, Selected: slices.Contains(state["functions"], item.PublicID) || slices.Contains(assigned, item.PublicID)})
		}
		a.Fields = []view.AdminField{f}
	case "assignments":
		title = "Zugeordnete Funktionen"
		for _, id := range assigned {
			for _, f := range functions {
				if f.PublicID == id {
					a.InviteAssignments = append(a.InviteAssignments, view.AdminOption{Value: id, Label: view.MemberRoleKind(f.Group) + " / " + f.Title})
				}
			}
		}
	case "details":
		title = "Wen möchtest du einladen?"
		a.SubmitLabel = "Einladung senden"
		a.Fields = []view.AdminField{view.Field("name", "Name", state.Get("name"), "text"), view.Field("email", "E-Mail-Adresse", state.Get("email"), "email")}
	}
	a.HiddenFields = append(a.HiddenFields, view.Field("step", "", step, "hidden"))
	for key, value := range map[string]string{"role": role, "kind": kind, "name": state.Get("name"), "email": state.Get("email")} {
		if role == "admin" && key == "kind" {
			continue
		}
		if !slices.ContainsFunc(a.Fields, func(f view.AdminField) bool { return f.ID == key }) {
			a.HiddenFields = append(a.HiddenFields, view.Field(key, "", value, "hidden"))
		}
	}
	for _, id := range assigned {
		a.HiddenFields = append(a.HiddenFields, view.Field("assigned", "", id, "hidden"))
	}
	view.AttachErrors(a.Fields, fields)
	status := http.StatusOK
	if len(fields) > 0 {
		status = http.StatusUnprocessableEntity
	}
	c.adminRender(w, title, a, status)
}
