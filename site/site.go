package site

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"

	"github.com/fachschaftinformatik/web/model/events"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/model/media"
	"github.com/fachschaftinformatik/web/model/meetings"
	"github.com/fachschaftinformatik/web/model/members"
	"github.com/fachschaftinformatik/web/model/organization"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) adminRender(w http.ResponseWriter, title string, a *view.AdminPage, status int) {
	a.CanResendConfirmation = a.User != nil && !a.User.Verified
	a.ReviewerOnly = !w.(*sessionResponse).session.CanAdmin()
	p := view.Page{Title: title, Active: "admin", Admin: a}
	if a.View == "users" && a.Pages > 1 {
		p.DocumentTitle = fmt.Sprintf("Benutzerkonten – Seite %d von %d", a.Page, a.Pages)
	}
	if a.View == "audit" && a.Pages > 1 {
		p.DocumentTitle = fmt.Sprintf("Aktivitäten – Seite %d von %d", a.Page, a.Pages)
	}
	if a.HasErrors() {
		p.DocumentTitle = "Fehler: " + title
	}
	c.renderStatus(w, "admin", false, p, status)
}

func (c *Handler) notFound(w http.ResponseWriter, _ *http.Request) {
	c.pageError(w, "Prüfe die Adresse. Wenn du einem Link auf unserer Website gefolgt bist, gib uns bitte Bescheid.", http.StatusNotFound)
}

func (c *Handler) pageError(w http.ResponseWriter, message string, status int) {
	title := "Die Anfrage konnte nicht verarbeitet werden"
	switch status {
	case http.StatusNotFound:
		title = "Seite nicht gefunden"
	case http.StatusForbidden:
		title = "Zugriff nicht möglich"
	case http.StatusInternalServerError, http.StatusServiceUnavailable:
		title = "Der Dienst ist gerade nicht verfügbar"
	}
	w.Header().Set("Cache-Control", "no-store")
	c.renderStatus(w, "error", false, view.Page{Title: title, Active: "error", Message: message}, status)
}

type Handler struct {
	buildVersion  string
	buildRevision string
	organization  *organization.Store
	router        http.Handler
	views         *view.Renderer
	members       *members.Store
	meetings      *meetings.Store
	identity      *users.Service
	secureCookies bool
	exams         *exams.Service
	posts         *posts.Service
	media         *media.Service
	events        *events.Service
	audit         *audit.Store
}

type Services struct {
	BuildVersion  string
	BuildRevision string
	Organization  *organization.Store
	Users         *users.Service
	Exams         *exams.Service
	Members       *members.Store
	Meetings      *meetings.Store
	Posts         *posts.Service
	Media         *media.Service
	Events        *events.Service
	Audit         *audit.Store
}

// New constructs the HTML application with its required dependencies.
func New(services Services, secureCookies bool) (*Handler, error) {
	if services.Organization == nil || services.Users == nil || services.Exams == nil || services.Members == nil || services.Meetings == nil || services.Posts == nil || services.Media == nil || services.Events == nil || services.Audit == nil {
		return nil, errors.New("application services are incomplete")
	}
	views, err := view.NewRenderer()
	if err != nil {
		return nil, err
	}
	c := &Handler{buildVersion: services.BuildVersion, buildRevision: services.BuildRevision, views: views, secureCookies: secureCookies, organization: services.Organization,
		identity: services.Users, exams: services.Exams, members: services.Members,
		meetings: services.Meetings, posts: services.Posts, media: services.Media, events: services.Events, audit: services.Audit}
	c.router = c.routes()
	return c, nil
}

func (c *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Apply defaults before routing so redirects and error pages are covered too.
	// SAMEORIGIN keeps the editor's same-origin iframe working.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "base-uri 'none'; object-src 'none'; frame-ancestors 'self'; form-action 'self'")
	c.router.ServeHTTP(w, r)
}

func (c *Handler) render(w http.ResponseWriter, page string, fragment bool, data view.Page) {
	c.renderStatus(w, page, fragment, data, http.StatusOK)
}

func (c *Handler) serverError(w http.ResponseWriter, err error) {
	log.Printf("Request failed: %v", err)
	c.pageError(w, "Versuche es später erneut. Deine Anfrage konnte nicht abgeschlossen werden.", http.StatusInternalServerError)
}

func (c *Handler) renderStatus(w http.ResponseWriter, page string, fragment bool, data view.Page, status int) {
	data.BuildVersion = c.buildVersion
	data.BuildRevision = c.buildRevision
	if status != http.StatusOK || fragment {
		data.CanonicalURL = ""
	} else if response, ok := w.(*sessionResponse); ok && data.CanonicalURL == "" {
		data.CanonicalURL = response.canonicalURL
	}
	if c.organization != nil {
		data.Organization = organizationView(c.organization.Current())
	}
	if c.identity != nil {
		domains := c.identity.Domains()
		data.RegistrationDomains = view.DomainLabels(domains.Registration)
		data.AccountDomains = view.DomainLabels(domains.Accounts)
	}
	if c.members != nil {
		for _, role := range c.members.Roles("board") {
			if role.ID == data.Organization.ChairRole {
				data.ChairEmail = role.Email
			}
		}
		for _, role := range c.members.Roles("departments") {
			if role.ID == data.Organization.PublicityRole {
				data.PublicityEmail = role.Email
			}
		}
	}
	if status == http.StatusUnprocessableEntity {
		if data.DocumentTitle == "" {
			data.DocumentTitle = data.Title
		}
		if !strings.HasPrefix(data.DocumentTitle, "Fehler: ") {
			data.DocumentTitle = "Fehler: " + data.DocumentTitle
		}
	}
	if response, ok := w.(*sessionResponse); ok && response.session != nil {
		data.SignedIn, data.CSRF = response.session.Authenticated, response.session.CSRF
		data.CanAdmin = response.session.CanReview()
		data.UserID, data.Role = response.session.UserID, response.session.Role
		if data.SignedIn {
			data.SessionDeadline = response.session.Deadline().UnixMilli()
			data.SessionAbsolute = response.session.Expires.UnixMilli()
			data.SessionNow = time.Now().UnixMilli()
		}
	}
	var html bytes.Buffer
	if err := c.views.Render(&html, page, fragment, data); err != nil {
		log.Printf("Render %s: %v", page, err)
		http.Error(w, "Die Seite konnte nicht geladen werden.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = html.WriteTo(w)
}

func ptr[T any](value T) *T { return &value }
