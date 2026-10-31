package view

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"

	"github.com/fachschaftinformatik/web/internal/content"
)

//go:embed templates/*.html assets/*
var resources embed.FS

type Renderer struct {
	pages map[string]*template.Template
}

func NewRenderer() (*Renderer, error) {
	r := &Renderer{pages: make(map[string]*template.Template)}
	for _, name := range []string{"landing", "team", "contact", "sitemap", "legal", "cookies", "feedback", "error", "exams", "examsettings", "module", "upload", "submission", "submissions", "media", "community", "about", "content", "elections", "regulations", "login", "register", "forgot", "token", "invite", "preferences", "posts", "thread", "profile", "news", "events", "editor", "admin", "sessionexpired"} {
		if pageContent(name, Page{}) == nil {
			return nil, fmt.Errorf("view %q has no content model", name)
		}
		t, err := template.New("layout").Funcs(template.FuncMap{"asset": AssetPath, "voteControl": voteControl, "assignmentSummary": assignmentSummary, "scopeField": scopeField, "poLabel": VersionLabel, "rawPostText": rawPostText, "richHTML": func(source string) template.HTML { return template.HTML(content.SanitizeRichText(source)) }}).ParseFS(resources, "templates/layout.html", "templates/sessionwarning.html", "templates/vote.html", "templates/submissionlist.html", "templates/examscope.html", "templates/assignmentsummary.html", "templates/adminnav.html", "templates/adminregulations.html", "templates/eventcards.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		r.pages[name] = t
	}
	return r, nil
}

func (r *Renderer) Render(w io.Writer, page string, fragment bool, data Page) error {
	if fragment {
		return r.RenderFragment(w, page, "results", data)
	}
	return r.RenderPage(w, page, data)
}

// RenderPage always renders a complete document using a feature-specific body.
func (r *Renderer) RenderPage(w io.Writer, page string, data Page) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("unknown view %q", page)
	}
	content := pageContent(page, data)
	return t.ExecuteTemplate(w, "layout", data.document(content))
}

// RenderFragment accepts only deliberately exposed targets. Shared helpers and
// arbitrary named templates are not public fragment entry points.
func (r *Renderer) RenderFragment(w io.Writer, page, target string, data Page) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("unknown view %q", page)
	}
	if page != "exams" || target != "results" {
		return fmt.Errorf("unknown fragment %q for view %q", target, page)
	}
	return t.ExecuteTemplate(w, target, pageContent(page, data))
}

func Assets() fs.FS {
	assets, _ := fs.Sub(resources, "assets")
	return assets
}
