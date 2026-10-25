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
	for _, name := range []string{"landing", "team", "contact", "sitemap", "legal", "error", "exams", "examsettings", "module", "upload", "submission", "submissions", "media", "community", "about", "content", "elections", "regulations", "login", "register", "forgot", "token", "invite", "posts", "thread", "profile", "news", "events", "editor", "admin", "sessionexpired"} {
		t, err := template.New("layout").Funcs(template.FuncMap{"voteControl": voteControl, "assignmentSummary": assignmentSummary, "scopeField": scopeField, "poLabel": VersionLabel, "rawPostText": rawPostText, "richHTML": func(source string) template.HTML { return template.HTML(content.SanitizeRichText(source)) }}).ParseFS(resources, "templates/layout.html", "templates/sessionwarning.html", "templates/vote.html", "templates/submissionlist.html", "templates/examscope.html", "templates/assignmentsummary.html", "templates/adminnav.html", "templates/eventcards.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		r.pages[name] = t
	}
	return r, nil
}

func (r *Renderer) Render(w io.Writer, page string, fragment bool, data Page) error {
	t, ok := r.pages[page]
	if !ok {
		return fmt.Errorf("unknown view %q", page)
	}
	name := "layout"
	if fragment {
		name = "results"
	}
	return t.ExecuteTemplate(w, name, data)
}

func Assets() fs.FS {
	assets, _ := fs.Sub(resources, "assets")
	return assets
}
