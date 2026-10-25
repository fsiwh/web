package view

import "net/url"

type RegulationDocument struct{ Title, URL, Date string }

func (d RegulationDocument) DateLabel() string { return dateLabel(d.Date) }

type Regulation struct {
	Program, Degree, Version, URL                               string
	PO, Handbook                                                RegulationDocument
	Amendments, Additional                                      []RegulationDocument
	SourceURL, HandbookSourceURL, FrameworkURL, CheckedAt, Note string
}

func (r Regulation) CheckedLabel() string { return dateLabel(r.CheckedAt) }

func (a AdminPage) RegulationFiltersActive() bool {
	return a.Query != "" || a.RegulationProgram != "" || a.RegulationDegree != ""
}
func (a AdminPage) RegulationPagination() ListPagination {
	q := url.Values{}
	if a.Query != "" {
		q.Set("search", a.Query)
	}
	if a.RegulationProgram != "" {
		q.Set("program", a.RegulationProgram)
	}
	if a.RegulationDegree != "" {
		q.Set("degree", a.RegulationDegree)
	}
	return ListPagination{Page: a.Page, Pages: a.Pages, Total: a.Total, Path: "/admin/regulations", Query: q}
}
