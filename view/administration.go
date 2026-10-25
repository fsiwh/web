package view

import (
	"net/url"
	"strconv"
)

type AdminMemberRow struct {
	Member
	Roles []string
}

type AdminRoleRow struct {
	Title, Kind, URL string
	Members          []string
}

func MemberRoleKind(group string) string {
	if group == "board" {
		return "Vorstand"
	}
	if group == "departments" {
		return "Referat"
	}
	return "Projektstelle"
}

type AdminLink struct{ ID, Label, URL, Group string }

var AdminLinks = []AdminLink{
	{"settings", "Stammdaten", "/admin/settings", "Organisation"},
	{"members", "Mitglieder", "/admin/members", "Organisation"},
	{"roles", "Funktionen", "/admin/roles", "Organisation"},
	{"meetings", "Sitzungen", "/admin/meetings", "Organisation"},
	{"modules", "Module", "/admin/modules", "Vorgänge"},
	{"programs", "Studiengänge", "/admin/programs", "Vorgänge"},
	{"regulations", "Prüfungsordnungen", "/admin/regulations", "Vorgänge"},
	{"submissions", "Einreichungen", "/admin/submissions", "Vorgänge"},
	{"users", "Benutzerkonten", "/admin/users", "Zugänge"},
	{"audit", "Aktivitäten", "/admin/audit", "Zugänge"},
}

type AdminPage struct {
	OrganizationSettings                                         []OrganizationSettingsTask
	CanResendConfirmation                                        bool
	Member                                                       *Member
	Inviting                                                     bool
	ReviewerOnly                                                 bool
	ReadOnly                                                     bool
	Submissions                                                  []Submission
	Section, SectionTitle, View, Action, Query, Message, Success string
	Fields                                                       []AdminField
	Members                                                      []AdminMemberRow
	Roles                                                        []AdminRoleRow
	Users                                                        []User
	Modules                                                      []AdminModule
	StudyPrograms                                                []AdminStudyProgram
	Regulations                                                  []Regulation
	Regulation                                                   *Regulation
	RegulationProgram, RegulationDegree                          string
	RegulationProgramOptions                                     []AdminOption
	ProgramDegree                                                string
	ModuleProgram, ModuleDegree                                  string
	ModuleProgramOptions, ModuleDegreeOptions                    []AdminOption
	AuditEntries                                                 []AuditEntry
	AuditEvent, AuditOutcome                                     string
	AuditEventOptions, AuditOutcomeOptions                       []AdminOption
	UserRoles                                                    []string
	UserStatus                                                   string
	User                                                         *User
	Meetings                                                     []Meeting
	Publication                                                  *Publication
	Page, Pages, Total                                           int
	PreviousURL, NextURL                                         string
}

type AdminGroup struct {
	Title string
	Links []AdminLink
}

func (a AdminPage) Groups() []AdminGroup {
	var groups []AdminGroup
	for _, link := range AdminLinks {
		if a.ReviewerOnly && link.ID == "users" {
			continue
		}
		if len(groups) == 0 || groups[len(groups)-1].Title != link.Group {
			groups = append(groups, AdminGroup{Title: link.Group})
		}
		group := &groups[len(groups)-1]
		group.Links = append(group.Links, link)
	}
	return groups
}

func (a AdminPage) SubmissionList() SubmissionListPage {
	return SubmissionListPage{Submissions: a.Submissions, Reviewing: true, CanViewUsers: !a.ReviewerOnly}
}

func (a AdminPage) UserPageURL(page int) string {
	q := url.Values{}
	for _, role := range a.UserRoles {
		q.Add("role", role)
	}
	if a.UserStatus != "" {
		q.Set("status", a.UserStatus)
	}
	if a.Query != "" {
		q.Set("search", a.Query)
	}
	if page != 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/admin/users"
	}
	return "/admin/users?" + q.Encode()
}

func (a AdminPage) UserFiltersActive() bool {
	return a.Query != "" || len(a.UserRoles) > 0 || a.UserStatus != ""
}

func (a AdminPage) UserRoleOptions() []AdminOption {
	options := []AdminOption{{Value: "admin", Label: "Administration"}, {Value: "editor", Label: "Redaktion"}, {Value: "user", Label: "Studierende"}}
	for i := range options {
		for _, role := range a.UserRoles {
			if role == options[i].Value {
				options[i].Selected = true
			}
		}
	}
	return options
}

func (a AdminPage) UserStatusOptions() []AdminOption {
	options := []AdminOption{{Value: "", Label: "Alle"}, {Value: "active", Label: "Aktiv"}, {Value: "verified", Label: "Bestätigt"}, {Value: "inactive", Label: "Inaktiv"}, {Value: "unverified", Label: "Nicht bestätigt"}}
	for i := range options {
		options[i].Selected = options[i].Value == a.UserStatus
	}
	return options
}

func (a AdminPage) Pagination() []PaginationItem {
	return paginationItems(a.Page, a.Pages, a.UserPageURL)
}

func (a AdminPage) HasErrors() bool {
	if a.Message != "" {
		return true
	}
	for _, Field := range a.Fields {
		if Field.Error != "" {
			return true
		}
	}
	return false
}
