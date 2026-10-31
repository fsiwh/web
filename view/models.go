package view

// CommonData is presentation state shared by the document and page bodies.
// It contains no feature aggregates or persistence services.
type CommonData struct {
	ShowCookieNotice                                                bool
	Organization                                                    Organization
	RegistrationDomains, AccountDomains, ChairEmail, PublicityEmail string
	Title, Active, DocumentTitle, Description, Message              string
	CanonicalURL, BuildLabel, BuildShortRevision, FeedbackPath      string
	SignedIn, CanAdmin, About, Community                            bool
	UserID                                                          int64
	Role, CSRF                                                      string
	SessionDeadline, SessionAbsolute, SessionNow                    int64
	Breadcrumbs                                                     []Breadcrumb
}

func (p Page) commonData() CommonData {
	return CommonData{
		ShowCookieNotice: p.ShowCookieNotice,
		Organization:     p.Organization, RegistrationDomains: p.RegistrationDomains,
		AccountDomains: p.AccountDomains, ChairEmail: p.ChairEmail, PublicityEmail: p.PublicityEmail,
		Title: p.Title, Active: p.Active, DocumentTitle: p.DocumentTitle, Description: p.Description, Message: p.Message,
		CanonicalURL: p.CanonicalURL, BuildLabel: p.BuildLabel(), BuildShortRevision: p.BuildShortRevision(),
		FeedbackPath: p.Feedback.Path, SignedIn: p.SignedIn, CanAdmin: p.CanAdmin, About: p.About, Community: p.Community,
		UserID: p.UserID, Role: p.Role, CSRF: p.CSRF,
		SessionDeadline: p.SessionDeadline, SessionAbsolute: p.SessionAbsolute, SessionNow: p.SessionNow,
		Breadcrumbs: p.Breadcrumbs(),
	}
}

func (p CommonData) voteIdentity() VoteIdentity {
	return VoteIdentity{SignedIn: p.SignedIn, CSRF: p.CSRF}
}

func (p Page) voteIdentity() VoteIdentity {
	return VoteIdentity{SignedIn: p.SignedIn, CSRF: p.CSRF}
}

// LayoutData separates layout decisions from feature data. Content is always one
// of the concrete models selected by pageContent, never the controller's Page.
type LayoutData struct {
	CommonData
	Content                                 any
	HasEditor, AdminNavigation, NarrowExams bool
	PendingSubmission                       bool
	PublishedAt, PublishedLabel             string
	Navigation                              SectionNavigation
}

type SectionNavigation struct {
	Active                                 string
	PostsNested, EventsNested, MediaNested bool
	TeamLinks                              []Breadcrumb
}

type SessionWarning struct {
	CSRF                                         string
	SessionDeadline, SessionAbsolute, SessionNow int64
}

func (d LayoutData) SessionWarning() SessionWarning {
	return SessionWarning{CSRF: d.CSRF, SessionDeadline: d.SessionDeadline, SessionAbsolute: d.SessionAbsolute, SessionNow: d.SessionNow}
}

func (p Page) document(content any) LayoutData {
	d := LayoutData{
		CommonData: p.commonData(), Content: content,
		HasEditor: p.Editor != nil, AdminNavigation: p.Admin != nil,
		NarrowExams:       p.Upload.Action != "" || p.Submission != nil,
		PendingSubmission: p.Submission != nil && !p.Submission.Reviewing && p.Submission.Entry.Status == "pending",
		Navigation: SectionNavigation{
			Active: p.Active, PostsNested: p.Posts.Thread != nil || p.Editor != nil,
			EventsNested: p.Events.Detail != nil || p.Posts.Thread != nil || p.Editor != nil,
			MediaNested:  p.Media.Detail,
		},
	}
	if p.Events.Detail != nil {
		d.PublishedAt, d.PublishedLabel = p.Events.Detail.SourcePublishedAt, p.Events.Detail.SourceDateLabel()
	}
	for _, group := range p.Team {
		title := group.Title
		if group.ID == "project" {
			title = "Projektstellen"
		}
		d.Navigation.TeamLinks = append(d.Navigation.TeamLinks, Breadcrumb{Title: title, URL: "/team#team-" + group.ID})
	}
	return d
}

type TeamContent struct {
	CommonData
	Team []TeamGroup
}

type LegalContent struct {
	CommonData
	Legal             LegalPage
	LegalUpdatedLabel string
}

type MeetingsContent struct {
	CommonData
	Meetings MeetingsPage
}

type ExamsContent struct {
	CommonData
	Exams ExamsPage
}

type UploadContent struct {
	CommonData
	Upload UploadPage
	Admin  *AdminPage
}

type SubmissionContent struct {
	CommonData
	Submission *SubmissionPage
	Admin      *AdminPage
}

type SubmissionsContent struct {
	CommonData
	SubmissionList SubmissionListPage
}

type MediaContent struct {
	CommonData
	Media MediaPage
}

type PostsContent struct {
	CommonData
	Posts PostsPage
}

type EventsContent struct {
	CommonData
	Events EventsPage
}

type EditorContent struct {
	CommonData
	Editor *ContentEditor
}

type LoginContent struct {
	CommonData
	Login LoginPage
}

type RegisterContent struct {
	CommonData
	Register RegisterPage
}

type ForgotContent struct {
	CommonData
	Forgot ForgotPage
}

type TokenContent struct {
	CommonData
	Token TokenPage
}

type InvitationContent struct {
	CommonData
	Invite InvitationPage
}

type FeedbackContent struct {
	CommonData
	Feedback FeedbackPage
}

type AdminContent struct {
	CommonData
	Admin *AdminPage
}

func pageContent(name string, p Page) any {
	c := p.commonData()
	switch name {
	case "landing":
		return p.LandingContent()
	case "team":
		return TeamContent{CommonData: c, Team: p.Team}
	case "legal":
		return LegalContent{CommonData: c, Legal: p.Legal, LegalUpdatedLabel: p.LegalUpdatedLabel()}
	case "content":
		return MeetingsContent{CommonData: c, Meetings: p.Meetings}
	case "exams", "module":
		return ExamsContent{CommonData: c, Exams: p.Exams}
	case "upload", "examsettings":
		return UploadContent{CommonData: c, Upload: p.Upload, Admin: p.Admin}
	case "submission":
		return SubmissionContent{CommonData: c, Submission: p.Submission, Admin: p.Admin}
	case "submissions":
		return SubmissionsContent{CommonData: c, SubmissionList: p.SubmissionList}
	case "media", "sitemap":
		return MediaContent{CommonData: c, Media: p.Media}
	case "posts", "news", "thread", "profile":
		return PostsContent{CommonData: c, Posts: p.Posts}
	case "events":
		return EventsContent{CommonData: c, Events: p.Events}
	case "editor":
		return EditorContent{CommonData: c, Editor: p.Editor}
	case "login":
		return LoginContent{CommonData: c, Login: p.Login}
	case "register":
		return RegisterContent{CommonData: c, Register: p.Register}
	case "forgot":
		return ForgotContent{CommonData: c, Forgot: p.Forgot}
	case "token":
		return TokenContent{CommonData: c, Token: p.Token}
	case "invite":
		return InvitationContent{CommonData: c, Invite: p.Invite}
	case "feedback":
		return FeedbackContent{CommonData: c, Feedback: p.Feedback}
	case "admin":
		return AdminContent{CommonData: c, Admin: p.Admin}
	case "contact", "community", "about", "cookies", "error", "elections", "regulations", "sessionexpired":
		return c
	default:
		return nil
	}
}
