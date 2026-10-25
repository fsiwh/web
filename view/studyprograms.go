package view

import "net/url"

type AdminStudyProgram struct {
	Name    string
	Degrees []string
	Modules int
}

func (a AdminPage) ProgramFiltersActive() bool { return a.Query != "" || a.ProgramDegree != "" }

func (a AdminPage) ProgramPagination() ListPagination {
	q := url.Values{}
	if a.Query != "" {
		q.Set("search", a.Query)
	}
	if a.ProgramDegree != "" {
		q.Set("degree", a.ProgramDegree)
	}
	return ListPagination{Page: a.Page, Pages: a.Pages, Total: a.Total, Path: "/admin/programs", Query: q}
}
