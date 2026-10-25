package view

import "net/url"

type AdminModule struct {
	Title, Program, Degree string
}

func (a AdminPage) ModuleFiltersActive() bool {
	return a.Query != "" || a.ModuleProgram != "" || a.ModuleDegree != ""
}

func (a AdminPage) ModulePagination() ListPagination {
	q := url.Values{}
	if a.Query != "" {
		q.Set("search", a.Query)
	}
	if a.ModuleProgram != "" {
		q.Set("program", a.ModuleProgram)
	}
	if a.ModuleDegree != "" {
		q.Set("degree", a.ModuleDegree)
	}
	return ListPagination{Page: a.Page, Pages: a.Pages, Total: a.Total, Path: "/admin/modules", Query: q}
}
