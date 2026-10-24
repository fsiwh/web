// Package urls declares the application's route tree without controller or model dependencies.
package urls

import (
	"net/url"
)

const (
	HomePath                   = "/"
	HomePattern                = "/{$}"
	HealthPath                 = "/healthz"
	AboutPath                  = "/about"
	TeamPath                   = "/team"
	SitemapPath                = "/sitemap"
	LegalPath                  = "/legal"
	PrivacyPath                = "/privacy"
	AccessibilityPath          = "/accessibility"
	ContactPath                = "/contact"
	ElectionsPath              = "/elections"
	RegulationsPath            = "/regulations"
	MeetingsPath               = "/meetings"
	CommunityPath              = "/community"
	PostsPath                  = "/posts"
	NewsPath                   = "/news"
	CreatePostPath             = "/posts/create"
	PostPattern                = "/posts/{post}"
	EditPostPattern            = PostPattern + "/edit"
	PostCommentsPattern        = PostPattern + "/comments"
	EditCommentPattern         = PostCommentsPattern + "/{comment}/edit"
	PostVotePattern            = PostPattern + "/vote"
	CommentVotePattern         = PostCommentsPattern + "/{comment}/vote"
	EventsPath                 = "/events"
	CreateEventPath            = "/events/create"
	EventPattern               = "/events/{event}"
	EditEventPattern           = EventPattern + "/edit"
	EventCoverPattern          = EventPattern + "/cover"
	MediaPath                  = "/media"
	ManageMediaPath            = MediaPath + "/manage"
	MediaVisibilityPattern     = MediaItemPattern + "/visibility"
	MediaItemPattern           = "/media/{image}"
	AlbumPattern               = "/media/albums/{album}"
	AlbumCoverPattern          = "/media/albums/{id}/cover/{size}"
	ImagePattern               = "/images/{id}/{size}"
	ExamsPath                  = "/exams"
	ExamModulePattern          = "/exams/modules/{module}"
	ExamDocumentPattern        = "/exams/documents/{id}"
	ExamSettingsPath           = "/exams/settings"
	CreateSubmissionPath       = "/exams/submissions/create"
	SubmissionsPath            = "/exams/submissions"
	SubmissionPattern          = "/exams/submissions/{submission}"
	CheckSubmissionPattern     = SubmissionPattern + "/check"
	EditSubmissionPattern      = SubmissionPattern + "/edit"
	UserPattern                = "/users/{id}"
	DocumentPattern            = "/documents/{id}"
	LoginPath                  = "/login"
	LogoutPath                 = "/logout"
	RegisterPath               = "/register"
	ForgotPasswordPath         = "/password/forgot"
	ResetPasswordPath          = "/password/reset"
	ConfirmEmailPath           = "/email/confirm"
	AcceptInvitationPath       = "/invitation/accept"
	AdminPath                  = "/admin"
	AdminSettingsPath          = "/admin/settings"
	AdminMembersPath           = "/admin/members"
	AdminRolesPath             = "/admin/roles"
	AdminMeetingsPath          = "/admin/meetings"
	AdminUsersPath             = "/admin/users"
	AdminAuditPath             = "/admin/audit"
	AdminSubmissionsPath       = "/admin/submissions"
	AdminMemberPattern         = "/admin/members/{id}"
	AdminRolePattern           = "/admin/roles/{id}"
	CreateAdminMemberPath      = AdminMembersPath + "/create"
	CreateAdminRolePath        = AdminRolesPath + "/create"
	CreateAdminMeetingPath     = AdminMeetingsPath + "/create"
	EditAdminMemberPattern     = AdminMemberPattern + "/edit"
	EditAdminRolePattern       = AdminRolePattern + "/edit"
	EditAdminMeetingPattern    = AdminMeetingPattern + "/edit"
	AdminMeetingPattern        = "/admin/meetings/{id}"
	AdminUserPattern           = "/admin/users/{id}"
	AdminSubmissionPattern     = "/admin/submissions/{submission}"
	ReviewSubmissionPattern    = AdminSubmissionPattern + "/review"
	EditAdminSubmissionPattern = AdminSubmissionPattern + "/edit"
	AssetsPath                 = "/assets/"
)

func segment(id string) string      { return url.PathEscape(id) }
func Post(id string) string         { return PostsPath + "/" + segment(id) }
func EditPost(id string) string     { return Post(id) + "/edit" }
func PostComments(id string) string { return Post(id) + "/comments" }
func EditComment(post, comment string) string {
	return PostComments(post) + "/" + segment(comment) + "/edit"
}
func PostVote(id string) string { return Post(id) + "/vote" }
func CommentVote(post, comment string) string {
	return PostComments(post) + "/" + segment(comment) + "/vote"
}
func Event(id string) string               { return EventsPath + "/" + segment(id) }
func EditEvent(id string) string           { return Event(id) + "/edit" }
func EventCover(id string) string          { return Event(id) + "/cover" }
func MediaItem(id string) string           { return MediaPath + "/" + segment(id) }
func MediaVisibility(id string) string     { return MediaItem(id) + "/visibility" }
func Album(id string) string               { return MediaPath + "/albums/" + segment(id) }
func AlbumCover(id, size string) string    { return Album(id) + "/cover/" + segment(size) }
func Image(id, size string) string         { return "/images/" + segment(id) + "/" + segment(size) }
func ExamModule(id string) string          { return ExamsPath + "/modules/" + segment(id) }
func ExamDocument(id string) string        { return ExamsPath + "/documents/" + segment(id) }
func Submission(id string) string          { return SubmissionsPath + "/" + segment(id) }
func EditSubmission(id string) string      { return Submission(id) + "/edit" }
func CheckSubmission(id string) string     { return Submission(id) + "/check" }
func AdminSubmission(id string) string     { return AdminSubmissionsPath + "/" + segment(id) }
func ReviewSubmission(id string) string    { return AdminSubmission(id) + "/review" }
func EditAdminSubmission(id string) string { return AdminSubmission(id) + "/edit" }
func Document(id string) string            { return "/documents/" + segment(id) }
func User(id string) string                { return "/users/" + segment(id) }
func AdminUser(id string) string           { return AdminUsersPath + "/" + segment(id) }
func AdminMember(id string) string         { return AdminMembersPath + "/" + segment(id) }
func AdminRole(id string) string           { return AdminRolesPath + "/" + segment(id) }
func AdminMeeting(id string) string        { return AdminMeetingsPath + "/" + segment(id) }
func EditAdminUser(id string) string       { return AdminUser(id) + "/reset" }
func EditAdminMember(id string) string     { return AdminMember(id) + "/edit" }
func EditAdminRole(id string) string       { return AdminRole(id) + "/edit" }
func EditAdminMeeting(id string) string    { return AdminMeeting(id) + "/edit" }
