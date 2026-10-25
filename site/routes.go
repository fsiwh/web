package site

import (
	"net/http"

	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/view"
)

// section fixes the dispatcher input at registration time; arbitrary URL sections
// must never grant access to the administrative dispatcher.
func section(name string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("section", name)
		handler(w, r)
	}
}

func (c *Handler) routes() http.Handler {
	router := http.NewServeMux()
	register := func(pattern string, policy routePolicy, handler http.HandlerFunc) {
		router.Handle(pattern, c.validateRoute(c.withSession(policy, handler)))
	}
	registerPages(c, register)
	registerPosts(c, register)
	registerEvents(c, register)
	registerMedia(c, register)
	registerExams(c, register)
	registerAccounts(c, register)
	register("GET /session", routePolicy{passive: true}, c.SessionStatus)
	register("POST /session/extend", routePolicy{passive: true, csrf: true, bodyLimit: 64 << 10}, c.ExtendSession)
	register("GET /session/expired", routePolicy{passive: true}, c.SessionExpired)
	registerAdmin(c, register)
	register("GET "+urls.DocumentPattern, routePolicy{}, c.Document)
	register("GET "+urls.AssetsPath, routePolicy{passive: true}, http.StripPrefix(urls.AssetsPath, http.FileServerFS(view.Assets())).ServeHTTP)
	// Unknown routes do not run authentication or CSRF middleware.
	router.HandleFunc("/", c.notFound)
	return c.canonicalize(router)
}

type routeRegister func(string, routePolicy, http.HandlerFunc)

func forms(register routeRegister, path string, policy routePolicy, handler http.HandlerFunc) {
	register("GET "+path, policy, handler)
	register("POST "+path, policy, handler)
}

func registerPages(c *Handler, register routeRegister) {
	public := routePolicy{}
	register("GET "+urls.HealthPath, routePolicy{passive: true}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	register("GET "+urls.HomePattern, public, func(w http.ResponseWriter, r *http.Request) {
		home, err := c.home(r.Context(), postsUser(w))
		if err != nil {
			c.serverError(w, err)
			return
		}
		albums, err := c.media.Carousel(r.Context())
		if err != nil {
			c.serverError(w, err)
			return
		}
		c.render(w, "landing", false, view.Page{Title: "Startseite", Active: "home", Home: home, Media: view.MediaPage{Albums: mapRecords(albums, albumView)}})
	})
	register("GET "+urls.TeamPath, public, c.Team)
	register("GET "+urls.SitemapPath, public, c.Sitemap)
	for _, path := range []string{urls.LegalPath, urls.PrivacyPath, urls.AccessibilityPath} {
		register("GET "+path, public, c.Legal)
	}
	register("GET "+urls.ContactPath, public, func(w http.ResponseWriter, r *http.Request) {
		c.render(w, "contact", false, view.Page{Title: "Kontakt", Active: "contact"})
	})
	for _, page := range []struct{ path, title, template string }{{urls.AboutPath, "Über uns", "about"}, {urls.ElectionsPath, "Wahlen", "elections"}, {urls.RegulationsPath, "Satzung und Ordnungen", "regulations"}} {
		register("GET "+page.path, public, func(w http.ResponseWriter, r *http.Request) {
			c.render(w, page.template, false, view.Page{Title: page.title, Active: page.template, About: true})
		})
	}
	register("GET "+urls.MeetingsPath, public, c.Meetings)
	register("GET "+urls.CommunityPath, public, func(w http.ResponseWriter, r *http.Request) {
		c.render(w, "community", false, view.Page{Title: "Community", Active: "community", Community: true})
	})
}

func registerExams(c *Handler, register routeRegister) {
	exam := routePolicy{authenticated: true, csrf: true, private: true, bodyLimit: exams.MaxUploadBytes + 1<<20}
	register("GET "+urls.ExamsPath, exam, c.Exams)
	register("GET "+urls.ExamModulePattern, exam, c.ExamsModule)
	register("GET "+urls.ExamDocumentPattern, exam, c.ExamsFile)
	forms(register, urls.ExamSettingsPath, exam, c.ExamSettings)
	forms(register, urls.CreateSubmissionPath, exam, c.UploadExams)
	register("GET "+urls.SubmissionsPath, exam, c.ExamsSubmissions)
	register("GET "+urls.SubmissionPattern, exam, c.ExamsSubmission)
	forms(register, urls.CheckSubmissionPattern, exam, c.ExamsSubmission)
	forms(register, urls.EditSubmissionPattern, exam, c.UploadExams)
}

func registerPosts(c *Handler, register routeRegister) {
	public := routePolicy{}
	member := routePolicy{authenticated: true, csrf: true, bodyLimit: 8 << 20}
	register("GET "+urls.PostsPath, public, c.Posts)
	register("GET "+urls.NewsPath, public, c.News)
	forms(register, urls.CreatePostPath, member, c.EditDiscussion)
	register("GET "+urls.PostPattern, public, c.Discussion)
	forms(register, urls.EditPostPattern, member, c.EditDiscussion)
	register("POST "+urls.PostCommentsPattern, member, c.Comment)
	register("POST "+urls.EditCommentPattern, member, c.Comment)
	register("POST "+urls.PostVotePattern, member, c.Vote)
	register("POST "+urls.CommentVotePattern, member, c.Vote)
}

func registerEvents(c *Handler, register routeRegister) {
	public := routePolicy{}
	member := routePolicy{authenticated: true, csrf: true, bodyLimit: 8 << 20}
	register("GET "+urls.EventsPath, public, c.Events)
	forms(register, urls.CreateEventPath, member, c.EditEvent)
	register("GET "+urls.EventPattern, public, c.Events)
	forms(register, urls.EditEventPattern, member, c.EditEvent)
	register("GET "+urls.EventCoverPattern, public, c.CalendarCover)
}

func registerMedia(c *Handler, register routeRegister) {
	public := routePolicy{}
	manager := routePolicy{authenticated: true, csrf: true, private: true, bodyLimit: 64 << 10}
	register("GET "+urls.ManageMediaPath, manager, c.ManageMedia)
	register("POST "+urls.MediaVisibilityPattern, manager, c.MediaVisibility)
	register("GET "+urls.MediaPath, public, c.Media)
	register("GET "+urls.AlbumPattern, public, c.Media)
	register("GET "+urls.AlbumCoverPattern, public, c.AlbumCover)
	register("GET "+urls.MediaItemPattern, public, c.Media)
	register("GET "+urls.ImagePattern, public, c.MediaFile)
}

func registerAccounts(c *Handler, register routeRegister) {
	account := routePolicy{session: true, csrf: true, bodyLimit: 64 << 10}
	member := routePolicy{authenticated: true, csrf: true, bodyLimit: 8 << 20}
	forms(register, urls.LoginPath, account, c.Login)
	register("POST "+urls.LogoutPath, member, c.Logout)
	forms(register, urls.RegisterPath, account, c.Register)
	forms(register, urls.ForgotPasswordPath, account, c.ForgotPassword)
	forms(register, urls.ResetPasswordPath, account, c.Token)
	forms(register, urls.ConfirmEmailPath, account, func(w http.ResponseWriter, r *http.Request) { r.SetPathValue("tokenKind", "verify"); c.Token(w, r) })
	forms(register, urls.AcceptInvitationPath, account, c.Invitation)
}

func registerAdmin(c *Handler, register routeRegister) {
	admin := routePolicy{authenticated: true, admin: true, csrf: true, bodyLimit: uploadBodyLimit}
	reviewer := routePolicy{authenticated: true, reviewer: true, csrf: true, bodyLimit: exams.MaxUploadBytes + 1<<20}
	form := func(path string, policy routePolicy, handler http.HandlerFunc) {
		forms(register, path, policy, handler)
	}
	register("GET "+urls.AdminPath, reviewer, c.Admin)
	register("GET "+urls.AdminAuditPath, admin, section("audit", c.Admin))
	register("GET "+urls.AdminModulesPath, admin, section("modules", c.Admin))
	register("GET "+urls.AdminProgramsPath, admin, section("programs", c.Admin))
	register("GET "+urls.AdminRegulationsPath, admin, section("regulations", c.Admin))
	register("GET "+urls.AdminRegulationPattern, admin, section("regulations", c.Admin))
	register("GET /admin/regulation-documents/{id}",admin,c.AdminRegulationDocument)
	for _, item := range []struct{ name, path string }{{"settings", urls.AdminSettingsPath}, {"members", urls.AdminMembersPath}, {"roles", urls.AdminRolesPath}, {"meetings", urls.AdminMeetingsPath}, {"users", urls.AdminUsersPath}} {
		register("GET "+item.path, admin, section(item.name, c.Admin))
	}
	for _, name := range []string{"identity", "contact", "legal", "links"} {
		handler := func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", name)
			section("settings", c.Admin)(w, r)
		}
		form(urls.AdminSettingsPath+"/"+name, admin, handler)
	}
	for _, item := range []struct{ name, create, edit string }{{"members", urls.CreateAdminMemberPath, urls.EditAdminMemberPattern}, {"roles", urls.CreateAdminRolePath, urls.EditAdminRolePattern}, {"meetings", urls.CreateAdminMeetingPath, urls.EditAdminMeetingPattern}} {
		handler := func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", "create")
			section(item.name, c.Admin)(w, r)
		}
		form(item.create, admin, handler)
		form(item.edit, admin, section(item.name, c.Admin))
	}
	register("GET "+urls.AdminMemberPattern, admin, section("members", c.Admin))
	register("POST "+urls.AdminMemberPattern+"/invite", admin, c.ResendInvitation)
	register("GET "+urls.AdminMeetingPattern, admin, section("meetings", c.Admin))
	form(urls.AdminMeetingPattern+"/check", admin, c.ReviewPublication)
	register("GET "+urls.AdminSubmissionsPath, reviewer, section("submissions", c.Admin))
	register("GET "+urls.AdminSubmissionPattern, reviewer, c.ReviewExamsSubmission)
	form(urls.ReviewSubmissionPattern, reviewer, c.ReviewExamsSubmission)
	form(urls.EditAdminSubmissionPattern, reviewer, section("submissions", c.UploadExams))
	register("GET "+urls.AdminUserPattern, admin, section("users", c.Admin))
	form(urls.AdminUserPattern+"/reset", admin, c.ResetUserPassword)
	register("POST "+urls.AdminUserPattern+"/confirm", admin, c.ResendUserConfirmation)
}
