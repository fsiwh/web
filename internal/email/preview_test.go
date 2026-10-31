package email

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/model/organization"
)

var emailPreviewDir = flag.String("email-preview-dir", "", "Export captured email previews to this directory")

// Exercise the actual send methods and MIME output through loopback SMTP. The
// optional export provides review fixtures without maintaining separate copy.
func TestEmailPreviews(t *testing.T) {
	db, err := database.NewConnection(testdb.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	details, err := json.Marshal(organization.Details{
		Name: "Fachschaft Informatik", FormalName: "Fachschaft Informatik", ShortName: "fsInformatik", Institution: "Westfälische Hochschule",
		City: "Beispielstadt", Street: "Beispielstraße 1", PostalCode: "12345", Country: "Deutschland",
		ContactEmail: "fachschaft@example.org", LegalUpdated: "2026-01-01",
		HostingName: "Beispielhost", HostingURL: "https://example.org/hosting", PrivacyURL: "https://example.org/privacy",
		SupervisionName: "Beispielaufsicht", SupervisionURL: "https://example.org/supervision",
		PrivacyAuthorityName: "Beispielbehörde", PrivacyAuthorityURL: "https://example.org/authority",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO organization(id,data) VALUES(1,?)`, string(details)); err != nil {
		t.Fatal(err)
	}
	org, err := organization.NewStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	const recipient, name = "alex@example.org", "Alex Müller"
	const domain = "https://fsi-wh.example"
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const verification = domain + "/email/confirm?token=" + token
	cases := []struct {
		ID, Label string
		Send      func(*Sender) error
	}{
		{"registration", "Registrierung bestätigen", func(s *Sender) error { return s.SendVerificationEmail(recipient, name, verification) }},
		{"email-change-confirmation", "Neue E-Mail-Adresse bestätigen", func(s *Sender) error { return s.SendVerificationEmail(recipient, name, verification) }},
		{"invitation", "Einladung annehmen", func(s *Sender) error {
			return s.SendVerificationEmail(recipient, name, domain+"/invitation/accept?token="+token)
		}},
		{"password-reset", "Passwort zurücksetzen", func(s *Sender) error {
			return s.SendPasswordResetEmail(recipient, name, domain+"/password/reset?token="+token)
		}},
		{"password-changed", "Passwort geändert", func(s *Sender) error { return s.SendAccountChangedEmail(recipient, name, "password_changed") }},
		{"email-changed", "E-Mail-Adresse geändert (an bisherige Adresse)", func(s *Sender) error { return s.SendAccountChangedEmail(recipient, name, "email_changed") }},
		{"submission-review", "Neue Einreichung zur Prüfung", func(s *Sender) error { return s.SendSubmissionEmail(recipient, name, "AbCdEfGhIjK") }},
		{"submission-approved", "Einreichung freigegeben", func(s *Sender) error { return s.SendSubmissionDecisionEmail(recipient, name, "AbCdEfGhIjK", true) }},
		{"submission-rejected", "Einreichung abgelehnt", func(s *Sender) error { return s.SendSubmissionDecisionEmail(recipient, name, "AbCdEfGhIjK", false) }},
		{"feedback-yes", "Website-Rückmeldung: Ja", func(s *Sender) error { return s.SendPageFeedback("/exams", "yes", "", "") }},
		{"feedback-no", "Website-Rückmeldung: Nein", func(s *Sender) error { return s.SendPageFeedback("/exams", "no", "", "") }},
		{"feedback-problem", "Website-Rückmeldung: Problem", func(s *Sender) error {
			return s.SendPageFeedback("/exams", "problem", "Ich wollte Lernmaterial finden.\nStudiengang: Informatik", "Die Liste blieb leer.\nAuch nach dem Neuladen.")
		}},
	}
	type preview struct{ ID, Label string }
	var previews []preview
	if *emailPreviewDir != "" {
		if err := os.MkdirAll(*emailPreviewDir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, data []byte) {
		t.Helper()
		if *emailPreviewDir != "" {
			if err := os.WriteFile(filepath.Join(*emailPreviewDir, name), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			raw := captureEmail(t, org, func(s *Sender) error {
				s.cfg.Domain = domain
				s.cfg.SMTPReplyTo = "fachschaft@example.org"
				return tc.Send(s)
			})
			message, err := mail.ReadMessage(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			previews = append(previews, preview{tc.ID, tc.Label})
			_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			parts := multipart.NewReader(message.Body, params["boundary"])
			for _, ext := range []string{"txt", "html"} {
				part, err := parts.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(body, []byte(domain)) || bytes.Contains(body, []byte("ZgotmplZ")) {
					t.Fatal("email lost its domain or contains an invalid template URL")
				}
				if ext == "html" && bytes.Count(body, []byte("<h1")) != 1 {
					t.Fatal("email must contain exactly one main heading")
				}
				write(tc.ID+"."+ext, body)
			}
			if _, err := parts.NextPart(); err != io.EOF {
				t.Fatal("unexpected MIME part", err)
			}
			write(tc.ID+".eml", raw)
		})
	}
	if *emailPreviewDir == "" {
		return
	}
	var index bytes.Buffer
	if err := template.Must(template.New("index").Parse(previewIndex)).Execute(&index, previews); err != nil {
		t.Fatal(err)
	}
	write("index.html", index.Bytes())
	t.Log("Email previews exported to", *emailPreviewDir)
}

const previewIndex = `<!doctype html>
<html lang="de"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>E-Mail-Vorschau</title>
<style>
body{font:18px/1.5 Arial,sans-serif;color:#0b0c0c;margin:0;background:#f3f2f1}
main{max-width:1100px;margin:auto;padding:24px} h1{margin-top:0} h2{font-size:24px}
a{color:#1d70b8} a:focus{outline:3px solid #ffdd00} nav ul{padding-left:22px}
section{background:white;padding:24px;margin:30px 0}
iframe{display:block;box-sizing:border-box;border:1px solid #b1b4b6;width:100%;height:850px;margin-top:20px;background:white}
.mobile iframe{max-width:390px} button{font:inherit;margin:0 8px 8px 0;padding:8px 16px;cursor:pointer}
</style><main><h1>E-Mail-Vorschau</h1>
<p>Aktuelle E-Mails aus dem echten Versandcode. Alle Beispiele verwenden fiktive Daten und ungültige Links.</p>
<p><button type="button" onclick="document.body.classList.remove('mobile')">Desktop</button><button type="button" onclick="document.body.classList.add('mobile')">Mobil (390 px)</button></p>
<nav aria-label="E-Mail-Typen"><ul>{{range .}}<li><a href="#{{.ID}}">{{.Label}}</a></li>{{end}}</ul></nav>
{{range .}}<section id="{{.ID}}"><h2>{{.Label}}</h2>
<p><a href="{{.ID}}.html">HTML öffnen</a> · <a href="{{.ID}}.txt">Textversion</a> · <a href="{{.ID}}.eml" download>EML herunterladen</a></p>
<iframe title="{{.Label}}" src="{{.ID}}.html" sandbox loading="lazy"></iframe></section>{{end}}
</main></html>`
