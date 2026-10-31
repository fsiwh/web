package site

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/view"
)

func (c *Handler) preferredExamsFilter(w http.ResponseWriter, r *http.Request) (exams.Filter, error) {
	filter := examsFilter(r)
	q := r.URL.Query()
	if q.Has("search") || q.Has("program") || q.Has("degree") || q.Has("version") {
		return filter, nil
	}
	session := w.(*sessionResponse).session
	if session == nil || !session.Authenticated {
		return filter, nil
	}
	preferred, err := c.exams.Settings(r.Context(), session.UserID)
	preferred.Query = filter.Query
	return preferred, err
}

func (c *Handler) ExamSettings(w http.ResponseWriter, r *http.Request) {

	user := w.(*sessionResponse).session.UserID
	account := r.URL.Path == urls.AccountStudiesPath
	if !account && r.Method == http.MethodPost && r.PostForm.Get("intent") == "submission-mail" {
		if err := c.exams.SaveSubmissionMailPreference(r.Context(), user, r.PostForm.Get("submission_mail") == "on"); err != nil {
			c.examsWriteError(w, r, err)
			return
		}
		flash(w, "Deine E-Mail-Benachrichtigungen wurden gespeichert.")
		http.Redirect(w, r, "/exams/settings", http.StatusSeeOther)
		return
	}
	filter, err := c.exams.Settings(r.Context(), user)
	if err != nil {
		c.examsWriteError(w, r, err)
		return
	}
	catalog, err := c.exams.Catalog(r.Context())
	if err != nil {
		c.serverError(w, err)
		return
	}
	status := 200
	page := view.Page{Title: "Einstellungen für Lernmaterial", Active: "exams", Upload: view.UploadPage{Action: r.URL.Path}}
	if account {
		page.Title, page.Active = "Studium", "account"
		page.Preferences.Section = "studies"
	} else {
		page.Upload.SubmissionMail, page.Upload.CanReceiveSubmissionMail, err = c.exams.SubmissionMailPreference(r.Context(), user)
		if err != nil {
			c.examsWriteError(w, r, err)
			return
		}
	}
	if r.Method == http.MethodPost {
		filter = exams.Filter{Programs: slices.DeleteFunc(r.PostForm["program"], func(value string) bool { return value == "" }), Degrees: r.PostForm["degree"], Versions: r.PostForm["version"]}
		if err := c.exams.SaveSettings(r.Context(), user, filter); err != nil {
			var fields exams.ValidationErrors
			if !errors.As(err, &fields) {
				c.examsWriteError(w, r, err)
				return
			}
			page.Upload.Errors = fields
			status = 422
			page.DocumentTitle = "Fehler: " + page.Title
		} else {
			if account {
				flash(w, "Deine Angaben zum Studium wurden gespeichert.")
				http.Redirect(w, r, urls.AccountPath, http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/exams", http.StatusSeeOther)
			return
		}
	}
	page.Upload.Programs = view.UploadOptions(catalog.Programs(), filter.Programs)
	page.Upload.Degrees = view.UploadOptions(catalog.Degrees(), filter.Degrees)
	for i := range page.Upload.Programs {
		page.Upload.Programs[i].Label = catalog.ProgramLabel(page.Upload.Programs[i].Name)
	}
	for i := range page.Upload.Degrees {
		page.Upload.Degrees[i].Label = exams.DegreeLabel(page.Upload.Degrees[i].Name)
	}
	page.Upload.Versions = view.UploadOptions(catalog.Versions(), filter.Versions)
	c.renderStatus(w, "examsettings", false, page, status)
}

func examsFilter(r *http.Request) exams.Filter {
	q := r.URL.Query()
	return exams.Filter{Query: q.Get("search"), Programs: slices.DeleteFunc(q["program"], func(value string) bool { return value == "" }), Versions: slices.DeleteFunc(q["version"], func(version string) bool { return version == "" }), Degrees: slices.DeleteFunc(q["degree"], func(value string) bool { return value == "" })}
}

func (c *Handler) Exams(w http.ResponseWriter, r *http.Request) {
	filter, err := c.preferredExamsFilter(w, r)
	if err != nil {
		c.serverError(w, err)
		return
	}
	pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageNumber = max(1, pageNumber)

	catalog, total, err := c.exams.CatalogPage(r.Context(), filter, pageNumber, view.ExamsPageSize)

	if err != nil {
		c.serverError(w, err)
		return
	}
	filter = catalog.NormalizeFilter(filter)
	if !c.validPageRange(w, r, total, view.ExamsPageSize) {
		return
	}
	programs := make([]view.FilterOption, 0)
	for _, program := range catalog.Programs() {
		programs = append(programs, view.FilterOption{Name: program, Label: catalog.ProgramLabel(program), Selected: slices.Contains(filter.Programs, program)})
	}
	versions := make([]view.FilterOption, 0)
	for _, version := range catalog.Versions() {
		versions = append(versions, view.FilterOption{Name: version, Selected: slices.Contains(filter.Versions, version)})
	}
	degrees := make([]view.FilterOption, 0)
	for _, degree := range catalog.Degrees() {
		degrees = append(degrees, view.FilterOption{Name: degree, Label: exams.DegreeLabel(degree), Selected: slices.Contains(filter.Degrees, degree)})
	}
	data := view.Page{
		Title: "Lernmaterial", Active: "exams",
		Exams: view.ExamsPage{Filter: examsFilterView(filter), Programs: programs, Versions: versions, Degrees: degrees, Modules: mapRecords(catalog.FindModules(filter), moduleView)},
	}

	data.Exams.Total = total
	data.Exams.Pages = max(1, (total+view.ExamsPageSize-1)/view.ExamsPageSize)
	data.Exams.Page = min(pageNumber, data.Exams.Pages)
	data.CanonicalURL = data.Exams.PageURL(data.Exams.Page)
	// Normalize submitted empty controls without losing a deliberate all-program
	// selection. The effective filter is encoded by the view's canonical builder.
	q := r.URL.Query()
	if q.Has("search") && q.Get("search") == "" || q.Has("program") && q.Get("program") == "" || q.Has("degree") && q.Get("degree") == "" || q.Has("version") && q.Get("version") == "" {
		http.Redirect(w, r, data.Exams.PageURL(data.Exams.Page), http.StatusSeeOther)
		return
	}

	if data.Exams.Pages > 1 {
		data.DocumentTitle = fmt.Sprintf("Lernmaterial (Seite %d von %d)", data.Exams.Page, data.Exams.Pages)
	}
	w.Header().Set("Vary", "HX-Request, HX-History-Restore-Request")
	fragment := r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
	c.render(w, "exams", fragment, data)
}

func (c *Handler) ExamsModule(w http.ResponseWriter, r *http.Request) {
	filter, err := c.preferredExamsFilter(w, r)
	if err != nil {
		c.serverError(w, err)
		return
	}

	module, total, err := c.exams.ModulePage(r.Context(), r.PathValue("module"), filter, requestedPage(r), 20)
	if errors.Is(err, exams.ErrNotFound) {
		c.notFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, err)
		return
	}
	presented := moduleView(module)
	session := w.(*sessionResponse).session
	for i, exam := range module.Exams {
		if session != nil {
			presented.Exams[i].CanRevise = exam.CanRevise(session.UserID, session.Role)
		}
	}
	page := view.ExamsPage{Module: &presented, Filter: examsFilterView(filter), ListPagination: listPagination(r, total, 20)}
	if !c.validPageRange(w, r, total, 20) {
		return
	}
	effective, err := url.Parse(page.SearchURL())
	if err != nil {
		c.serverError(w, err)
		return
	}
	page.ListPagination.Query = effective.Query()
	c.render(w, "module", false, view.Page{Title: module.Title, Active: "exams", Exams: page, CanonicalURL: page.ListPagination.PageURL(page.ListPagination.Page)})
}

func requestedPage(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return max(1, n)
}

func listPagination(r *http.Request, total, size int, keys ...string) view.ListPagination {
	pages := max(1, (total+size-1)/size)
	query := url.Values{}
	for _, key := range keys {
		for _, value := range r.URL.Query()[key] {
			if value != "" {
				query.Add(key, value)
			}
		}
	}
	return view.ListPagination{Page: min(requestedPage(r), pages), Pages: pages, Total: total, Path: r.URL.Path, Query: query}
}
