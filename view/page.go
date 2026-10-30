package view

import (
	"regexp"
	"strings"
	"time"
)

var buildCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (p Page) BuildLabel() string {
	if p.BuildVersion == "" {
		return "dev"
	}
	if buildCommit.MatchString(p.BuildVersion) {
		return p.BuildVersion[:7]
	}
	return strings.TrimPrefix(p.BuildVersion, "v")
}

func (p Page) BuildShortRevision() string {
	if p.BuildRevision != p.BuildVersion && buildCommit.MatchString(p.BuildRevision) {
		return p.BuildRevision[:7]
	}
	return ""
}

func (p Page) LegalUpdatedLabel() string {
	value := p.Legal.Updated
	if value == "" {
		value = p.Organization.LegalUpdated
	}
	d, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return ""
	}
	return dateLabel(d.Format(time.RFC3339))
}

type Breadcrumb struct {
	Title, URL string
}

// Breadcrumbs describes ancestors only; the current page is already the h1.
func (p Page) Breadcrumbs() []Breadcrumb {
	if p.Active == "home" {
		return nil
	}
	items := []Breadcrumb{{"Startseite", "/"}}
	if p.Admin != nil {
		if p.Admin.Section != "" {
			items = append(items, Breadcrumb{"Verwaltung", "/admin"})
		}
		if p.Admin.View == "form" || p.Admin.View == "settings-readonly" || p.Admin.View == "user" || p.Admin.View == "review" || p.Admin.View == "reset" || p.Admin.View == "regulation" {
			items = append(items, Breadcrumb{p.Admin.SectionTitle, "/admin/" + p.Admin.Section})
		}
		if p.Admin.View == "reset" {
			items = append(items, Breadcrumb{p.Admin.User.Name, p.Admin.User.URL()})
		}
	}
	if p.Exams.Module != nil {
		items = append(items, Breadcrumb{"Lernmaterial", p.Exams.SearchURL()})
	}
	if p.Active == "exams" && (p.Upload.Action != "" || p.Submission != nil || p.SubmissionList.Submissions != nil) {
		items = append(items, Breadcrumb{"Lernmaterial", "/exams"})
		if p.Submission != nil {
			items = append(items, Breadcrumb{"Meine Einreichungen", "/exams/submissions"})
		}
	}
	if p.About && p.Active != "about" {
		items = append(items, Breadcrumb{"Über uns", "/about"})
	}
	if p.Community && p.Active != "community" {
		items = append(items, Breadcrumb{"Community", "/community"})
	}
	if p.Active == "posts" && (p.Posts.Thread != nil || p.Editor != nil) {
		url := "/posts"
		if p.Posts.BackURL != "" {
			url = p.Posts.BackURL
		}
		items = append(items, Breadcrumb{"Forum", url})
		if p.Editor != nil && p.Editor.Editing {
			items = append(items, Breadcrumb{p.Editor.Draft.Title, p.Editor.BackURL})
		}
	}
	if p.Active == "events" && (p.Posts.Thread != nil || p.Editor != nil) {
		items = append(items, Breadcrumb{"Veranstaltungen", EventsURL})
		if p.Editor != nil && p.Editor.Editing {
			items = append(items, Breadcrumb{p.Editor.Draft.Title, p.Editor.BackURL})
		}
	}
	if p.Active == "events" && p.Events.Detail != nil {
		items = append(items, Breadcrumb{"Veranstaltungen", EventsURL})
	}
	if p.Active == "news" && (p.Posts.Thread != nil || p.Editor != nil) {
		items = append(items, Breadcrumb{"Ankündigungen", NewsURL})
	}
	if p.Media.Detail {
		items = append(items, Breadcrumb{"Fotos", "/media"})
		if p.Media.Viewer {
			items = append(items, Breadcrumb{p.Media.Album.Title, p.Media.Return})
		}
	}
	if p.Active == "register" || p.Active == "forgot" {
		items = append(items, Breadcrumb{"Anmelden", "/login"})
	}
	return items
}

const EventsURL = "/events"

type LegalPage struct {
	Updated string
	Kind    string
	Chair   []Member
}

const NewsURL = "/news"

type Page struct {
	Feedback                                                        FeedbackPage
	BuildVersion                                                    string
	BuildRevision                                                   string
	CanonicalURL                                                    string
	Organization                                                    Organization
	RegistrationDomains, AccountDomains, ChairEmail, PublicityEmail string
	Title, Active                                                   string
	DocumentTitle                                                   string
	Message                                                         string
	Legal                                                           LegalPage
	Community                                                       bool
	About                                                           bool
	Team                                                            []TeamGroup
	Exams                                                           ExamsPage
	Media                                                           MediaPage
	Home                                                            HomePage
	Login                                                           LoginPage
	Register                                                        RegisterPage
	Forgot                                                          ForgotPage
	Meetings                                                        MeetingsPage
	Posts                                                           PostsPage
	Events                                                          EventsPage
	Editor                                                          *ContentEditor
	SignedIn                                                        bool
	CanAdmin                                                        bool
	Token                                                           TokenPage
	Upload                                                          UploadPage
	Submission                                                      *SubmissionPage
	SubmissionList                                                  SubmissionListPage
	Invite                                                          InvitationPage
	UserID                                                          int64
	Role                                                            string
	CSRF                                                            string
	SessionDeadline, SessionAbsolute, SessionNow                    int64
	Admin                                                           *AdminPage
}
