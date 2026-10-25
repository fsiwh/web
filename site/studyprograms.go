package site

import (
	"net/http"
	"strings"

	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) AdminStudyPrograms(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	directory, err := c.exams.StudyPrograms(r.Context())
	if err != nil {
		c.serverError(w, err)
		return
	}
	a.View, a.Page, a.Query = "programs", requestedPage(r), strings.TrimSpace(r.URL.Query().Get("search"))
	switch degree := r.URL.Query().Get("degree"); degree {
	case "bachelor", "master":
		a.ProgramDegree = degree
	}
	page, ok := directory.Filter(a.Query, a.ProgramDegree, a.Page)
	if !c.validPageRange(w, r, page.Total, exams.StudyProgramsPerPage) {
		return
	}
	if !ok {
		c.notFound(w, r)
		return
	}
	a.Total, a.Pages = page.Total, page.Pages
	for _, program := range page.Programs {
		row := view.AdminStudyProgram{Name: program.Name, Modules: program.Modules}
		for _, degree := range program.Degrees {
			row.Degrees = append(row.Degrees, exams.DegreeLabel(degree))
		}
		a.StudyPrograms = append(a.StudyPrograms, row)
	}
	c.adminRender(w, a.SectionTitle, a, http.StatusOK)
}
