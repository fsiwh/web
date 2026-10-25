package site

import (
	"net/http"
	"slices"
	"strings"

	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) AdminModules(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	catalog, err := c.exams.ModuleCatalog(r.Context())
	if err != nil {
		c.serverError(w, err)
		return
	}
	a.View, a.Page, a.Query = "modules", requestedPage(r), strings.TrimSpace(r.URL.Query().Get("search"))
	if program := r.URL.Query().Get("program"); slices.Contains(catalog.Programs(), program) {
		a.ModuleProgram = program
	}
	if degree := r.URL.Query().Get("degree"); slices.Contains(catalog.Degrees(), degree) {
		a.ModuleDegree = degree
	}
	for _, program := range catalog.Programs() {
		a.ModuleProgramOptions = append(a.ModuleProgramOptions, view.AdminOption{Value: program, Label: catalog.ProgramLabel(program), Selected: program == a.ModuleProgram})
	}
	for _, degree := range catalog.Degrees() {
		a.ModuleDegreeOptions = append(a.ModuleDegreeOptions, view.AdminOption{Value: degree, Label: exams.DegreeLabel(degree), Selected: degree == a.ModuleDegree})
	}
	page, ok := catalog.ModulePage(exams.ModuleFilter{Query: a.Query, Program: a.ModuleProgram, Degree: a.ModuleDegree}, a.Page)
	if !c.validPageRange(w, r, page.Total, exams.ModulesPerPage) {
		return
	}
	if !ok {
		c.notFound(w, r)
		return
	}
	a.Total, a.Pages = page.Total, page.Pages
	for _, module := range page.Modules {
		program, degree := module.Qualification()
		a.Modules = append(a.Modules, view.AdminModule{Title: module.Title, Program: program, Degree: degree})
	}
	c.adminRender(w, a.SectionTitle, a, http.StatusOK)
}
