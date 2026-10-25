package site

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) AdminRegulationDocument(w http.ResponseWriter, r *http.Request) {
	object, filename, err := c.exams.RegulationDocument(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		c.notFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	defer object.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))
	w.Header().Set("Content-Length", strconv.FormatInt(object.Info.Size, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != "HEAD" {
		_, _ = io.Copy(w, object)
	}
}

func regulationView(record exams.Regulation) view.Regulation {
	row := view.Regulation{Program: record.Program, Degree: record.Degree, Version: record.Version, URL: urls.AdminRegulation(record.ProgramID, record.Version)}
	if source := record.Source; source != nil {
		convert := func(d exams.RegulationDocument) view.RegulationDocument {
			return view.RegulationDocument{Title: d.Title, URL: d.URL, Date: d.Date}
		}
		row.PO, row.Handbook = convert(source.PO), convert(source.Handbook)
		for i, d := range source.Amendments {
			doc := convert(d)
			doc.Title = fmt.Sprintf("%d. Änderungssatzung", i+1)
			row.Amendments = append(row.Amendments, doc)
		}
		for _, d := range source.Additional {
			row.Additional = append(row.Additional, convert(d))
		}
		row.SourceURL, row.HandbookSourceURL, row.FrameworkURL = source.SourceURL, source.HandbookSourceURL, source.FrameworkURL
		row.CheckedAt, row.Note = source.CheckedAt(), source.Note
	}
	return row
}

func (c *Handler) AdminRegulations(w http.ResponseWriter, r *http.Request, a *view.AdminPage) {
	directory, err := c.exams.Regulations(r.Context())
	if err != nil {
		c.serverError(w, err)
		return
	}
	if program := r.PathValue("program"); program != "" {
		record, ok := directory.Get(program, r.URL.Query().Get("version"))
		if !ok {
			c.notFound(w, r)
			return
		}
		row := regulationView(record)
		a.View, a.Regulation = "regulation", &row
		c.adminRender(w, "PO "+record.Version+" – "+record.Program+" ("+record.Degree+")", a, http.StatusOK)
		return
	}
	a.View, a.Page, a.Query = "regulations", requestedPage(r), strings.TrimSpace(r.URL.Query().Get("search"))
	if program := r.URL.Query().Get("program"); slices.Contains(directory.Programs(), program) {
		a.RegulationProgram = program
	}
	switch degree := r.URL.Query().Get("degree"); degree {
	case "bachelor", "master":
		a.RegulationDegree = degree
	}
	for _, program := range directory.Programs() {
		a.RegulationProgramOptions = append(a.RegulationProgramOptions, view.AdminOption{Value: program, Label: program, Selected: program == a.RegulationProgram})
	}
	page, ok := directory.Filter(exams.RegulationFilter{Query: a.Query, Program: a.RegulationProgram, Degree: a.RegulationDegree}, a.Page)
	if !c.validPageRange(w, r, page.Total, exams.RegulationsPerPage) {
		return
	}
	if !ok {
		c.notFound(w, r)
		return
	}
	a.Total, a.Pages = page.Total, page.Pages
	for _, record := range page.Records {
		a.Regulations = append(a.Regulations, regulationView(record))
	}
	c.adminRender(w, a.SectionTitle, a, http.StatusOK)
}
