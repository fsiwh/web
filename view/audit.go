package view

import (
	"fmt"

	"github.com/fachschaftinformatik/web/internal/urls"
)

type AuditAccount struct {
	ID             int64
	Name, PublicID string
}

func (a AuditAccount) Label() string {
	if a.Name != "" {
		return a.Name
	}
	if a.ID != 0 {
		return fmt.Sprintf("Gelöschtes Konto (%d)", a.ID)
	}
	return "Nicht zugeordnet"
}

func (a AuditAccount) URL() string {
	if a.PublicID == "" {
		return ""
	}
	return urls.AdminUser(a.PublicID)
}

type AuditEntry struct {
	OccurredAt, Event, Outcome string
	Actor, User                AuditAccount
}

func (e AuditEntry) TimeLabel() string { return postedLabel(e.OccurredAt) }

func (a AdminPage) AuditPagination() ListPagination {
	return ListPagination{Page: a.Page, Pages: a.Pages, Total: a.Total, Path: urls.AdminAuditPath}
}
