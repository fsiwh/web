package site

import (
	"net/http"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) AuditEvents(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	a.View, a.Page = "audit", requestedPage(r)
	page, err := c.audit.List(r.Context(), a.Page)
	if err != nil {
		c.serverError(w, err)
		return
	}
	if !c.validPageRange(w, r, page.Total, audit.PerPage) {
		return
	}
	a.Total, a.Pages = page.Total, max(1, (page.Total+audit.PerPage-1)/audit.PerPage)
	for _, entry := range page.Entries {
		a.AuditEntries = append(a.AuditEntries, view.AuditEntry{
			OccurredAt: entry.OccurredAt, Event: entry.Event, Outcome: entry.Outcome,
			Actor: view.AuditAccount{ID: entry.Actor.ID, Name: entry.Actor.Name, PublicID: entry.Actor.PublicID},
			User:  view.AuditAccount{ID: entry.User.ID, Name: entry.User.Name, PublicID: entry.User.PublicID},
		})
	}
	c.adminRender(w, a.SectionTitle, a, http.StatusOK)
}
