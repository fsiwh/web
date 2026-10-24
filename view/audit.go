package view

import (
	"fmt"
	"net/url"

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
	return "–"
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

func (e AuditEntry) TimeLabel() string {
	date, clock := ScheduleFields(e.OccurredAt)
	return date + ", " + clock
}

func (e AuditEntry) Account() AuditAccount {
	if e.User.ID != 0 {
		return e.User
	}
	return e.Actor
}

func (e AuditEntry) HasSeparateActor() bool {
	return e.Actor.ID != 0 && e.User.ID != 0 && e.Actor.ID != e.User.ID
}

func (e AuditEntry) EventLabel() string   { return AuditEventLabel(e.Event) }
func (e AuditEntry) OutcomeLabel() string { return AuditOutcomeLabel(e.Outcome) }

func AuditEventLabel(event string) string {
	labels := map[string]string{
		"account.login": "Anmeldung", "account.logout": "Abmeldung",
		"account.register": "Registrierung", "account.password_reset_requested": "Passwort angefordert",
		"account.password_reset": "Passwort zurückgesetzt", "account.email_confirmed": "E-Mail bestätigt",
		"account.confirmation_resent": "Bestätigung versendet", "account.invitation_accepted": "Einladung angenommen",
		"account.mail": "E-Mail-Versand", "account.token": "Bestätigungslink",
		"account.tokens_expired": "Abgelaufene Links", "session.created": "Sitzung erstellt",
		"session.revoked": "Sitzung beendet", "session.expired": "Sitzung abgelaufen",
		"access.denied": "Zugriffsprüfung", "http.request": "Seitenaufruf", "request.csrf": "Formularprüfung",
		"submission.submitted": "Einreichung eingegangen", "submission.reviewed": "Einreichung geprüft",
		"submission.edited": "Einreichung bearbeitet", "submission.draft_saved": "Entwurf gespeichert",
		"submission.mail_preference": "E-Mail-Einstellung", "submission.mail": "Einreichungsbenachrichtigung",
		"submission.decision_mail": "Prüfergebnis versendet", "exam_document.access": "Dokumentzugriff",
	}
	if label, ok := labels[event]; ok {
		return label
	}
	return event
}

func AuditOutcomeLabel(outcome string) string {
	labels := map[string]string{
		"success": "Erfolgreich", "granted": "Erlaubt", "rejected": "Abgelehnt",
		"login_required": "Anmeldung nötig", "insufficient_role": "Keine Berechtigung",
		"invalid_email": "Ungültige E-Mail", "account_not_found": "Konto unbekannt",
		"incorrect_password": "Falsches Passwort", "inactive": "Inaktiv", "unverified": "Nicht bestätigt",
		"invalid_form": "Ungültige Eingabe", "invalid_link": "Ungültiger Link",
		"invalid_or_expired_link": "Link ungültig/abgelaufen", "email_exists": "E-Mail vergeben",
		"verification_queued": "Bestätigung vorgemerkt", "mail_queued": "Versand vorgemerkt",
		"smtp_failed": "Versand fehlgeschlagen", "smtp_accepted": "Versand angenommen",
		"purged": "Bereinigt", "expired_or_revoked": "Abgelaufen/widerrufen",
		"account_access_revoked": "Zugang entzogen", "password_reset": "Passwort zurückgesetzt",
		"invitation_accepted": "Einladung angenommen", "expired": "Abgelaufen",
		"client_error": "Anfragefehler", "server_error": "Serverfehler", "error": "Fehlgeschlagen",
	}
	if label, ok := labels[outcome]; ok {
		return label
	}
	return outcome
}

func (e AuditEntry) OutcomeColour() string {
	switch e.Outcome {
	case "success", "granted", "smtp_accepted":
		return "green"
	case "rejected", "login_required", "insufficient_role", "invalid_email", "account_not_found", "incorrect_password", "invalid_form", "invalid_link", "invalid_or_expired_link", "email_exists", "smtp_failed", "client_error", "server_error", "error":
		return "red"
	case "mail_queued", "verification_queued":
		return "blue"
	default:
		return "grey"
	}
}

func (a AdminPage) AuditPagination() ListPagination {
	q := url.Values{}
	if a.Query != "" {
		q.Set("search", a.Query)
	}
	if a.AuditEvent != "" {
		q.Set("event", a.AuditEvent)
	}
	if a.AuditOutcome != "" {
		q.Set("outcome", a.AuditOutcome)
	}
	return ListPagination{Page: a.Page, Pages: a.Pages, Total: a.Total, Path: urls.AdminAuditPath, Query: q}
}

func (a AdminPage) AuditFiltersActive() bool {
	return a.Query != "" || a.AuditEvent != "" || a.AuditOutcome != ""
}
