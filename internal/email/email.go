package email

import (
	"bytes"
	"crypto/tls"
	"embed"
	"fmt"
	"html/template"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/fachschaftinformatik/web/internal/config"
	"github.com/fachschaftinformatik/web/internal/urls"
	"github.com/fachschaftinformatik/web/model/organization"
)

//go:embed templates/*.html
var templateFS embed.FS

type Sender struct {
	cfg          *config.Config
	organization *organization.Store
	tpl          *template.Template
}

func NewSender(cfg *config.Config, org *organization.Store) *Sender {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"lines": func(value string) []string {
			return strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
		},
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		panic(fmt.Errorf("failed to parse email templates: %w", err))
	}

	return &Sender{
		cfg: cfg, organization: org,
		tpl: tpl,
	}
}

type notification struct {
	Organization                                             organization.Details
	Name, Domain, Title, Intro, Action, Link, Validity, Note string
}

func (s *Sender) SendVerificationEmail(toEmail, name, link string) error {
	n := notification{
		Name: name, Link: link,
		Title:    "E-Mail-Adresse bestätigen",
		Intro:    "Bestätige deine E-Mail-Adresse, um dein Benutzerkonto zu nutzen.",
		Action:   "E-Mail-Adresse bestätigen",
		Validity: "Der Link ist 24 Stunden gültig.",
		Note:     "Wenn du diese E-Mail nicht angefordert hast, kannst du sie ignorieren.",
	}
	if target, err := url.Parse(link); err == nil && target.Path == "/invitation/accept" {
		n.Title = "Einladung zur Fachschaft"
		n.Intro = "Du wurdest als Mitglied der Fachschaft eingeladen. Lege ein Passwort fest und bestätige deine E-Mail-Adresse."
		n.Action = "Einladung annehmen"
		n.Note = "Bei Fragen zu deiner Einladung wende dich an die Fachschaft."
	}
	return s.notify(toEmail, n)
}

func (s *Sender) SendPasswordResetEmail(toEmail, name, link string) error {
	return s.notify(toEmail, notification{
		Name: name, Link: link,
		Title:    "Passwort zurücksetzen",
		Intro:    "Du kannst ein neues Passwort für dein Benutzerkonto festlegen.",
		Action:   "Passwort zurücksetzen",
		Validity: "Der Link ist eine Stunde gültig.",
		Note:     "Wenn du kein neues Passwort angefordert hast, kannst du diese E-Mail ignorieren. Dein bisheriges Passwort bleibt gültig.",
	})
}

func (s *Sender) SendAccountChangedEmail(toEmail, name, kind string) error {
	n := notification{
		Name: name, Link: s.cfg.Domain + urls.ForgotPasswordPath,
		Title:  "Dein Passwort wurde geändert",
		Intro:  "Das Passwort für dein Benutzerkonto wurde geändert.",
		Action: "Passwort zurücksetzen",
		Note:   "Wenn du diese Änderung nicht vorgenommen hast, setze dein Passwort zurück und wende dich an die Fachschaft.",
	}
	if kind == "email_changed" {
		n.Title = "Deine E-Mail-Adresse wurde geändert"
		n.Intro = "Deine neue E-Mail-Adresse wurde bestätigt und für dein Benutzerkonto übernommen. Diese Nachricht geht an deine bisherige Adresse."
		n.Link = s.cfg.Domain + urls.ContactPath
		n.Action = "Kontakt aufnehmen"
		n.Note = "Wenn du diese Änderung nicht vorgenommen hast, wende dich an die Fachschaft."
	}
	return s.notify(toEmail, n)
}

func (s *Sender) SendSubmissionEmail(toEmail, name, submissionID string) error {
	return s.notify(toEmail, notification{
		Name: name, Link: s.cfg.Domain + urls.ReviewSubmission(submissionID),
		Title:  "Neue Einreichung zur Prüfung",
		Intro:  "Neues Lernmaterial wartet auf deine Prüfung. Melde dich an, um die Einreichung zu prüfen.",
		Action: "Einreichung prüfen",
		Note:   "Du hast Benachrichtigungen über neue Einreichungen aktiviert. Du kannst sie in den Einstellungen für Lernmaterial deaktivieren.",
	})
}

func (s *Sender) SendSubmissionDecisionEmail(toEmail, name, submissionID string, approved bool) error {
	n := notification{
		Name: name, Link: s.cfg.Domain + urls.Submission(submissionID),
		Title:  "Deine Einreichung wurde abgelehnt",
		Intro:  "Deine Einreichung wurde abgelehnt. Melde dich an, um den Status anzusehen.",
		Action: "Einreichung ansehen",
		Note:   "Bei Fragen zu dieser Entscheidung wende dich an die Fachschaft.",
	}
	if approved {
		n.Title = "Deine Einreichung wurde freigegeben"
		n.Intro = "Deine Einreichung wurde freigegeben. Vielen Dank für deinen Beitrag!"
	}
	return s.notify(toEmail, n)
}

func (s *Sender) notify(to string, n notification) error {
	n.Domain = s.cfg.Domain
	message, err := s.message(to, n)
	if err != nil {
		return err
	}
	var auth smtp.Auth
	if s.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, s.cfg.SMTPHost)
	}
	addr := net.JoinHostPort(s.cfg.SMTPHost, s.cfg.SMTPPort)
	if err := send(addr, s.cfg.SMTPHost, auth, s.cfg.SMTPFrom, to, message); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}
	return nil
}

func (s *Sender) SendPageFeedback(path, value, expected, actual string) error {
	if s.cfg.SMTPHost == "" {
		return fmt.Errorf("SMTP is not configured")
	}
	answer := map[string]string{"yes": "Ja", "no": "Nein", "problem": "Problemmeldung"}[value]
	n := notification{
		Name: "Fachschaft", Title: "Website-Rückmeldung: " + answer,
		Intro:  "Seite: " + path + "\nRückmeldung: " + answer,
		Action: "Seite ansehen", Link: s.cfg.Domain + path,
	}
	if value == "problem" {
		n.Validity = "Was wolltest du tun?\n" + expected
		n.Note = "Was ist passiert?\n" + actual
	}
	return s.notify(s.organization.Current().ContactEmail, n)
}

func (s *Sender) message(to string, n notification) ([]byte, error) {
	if strings.ContainsAny(n.Title, "\r\n") {
		return nil, fmt.Errorf("invalid email subject")
	}
	if s.organization != nil {
		n.Organization = s.organization.Current()
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil || strings.ContainsAny(to, "\r\n") {
		return nil, fmt.Errorf("invalid recipient address")
	}
	from, err := mail.ParseAddress(s.cfg.SMTPFrom)
	if err != nil || strings.ContainsAny(s.cfg.SMTPFrom, "\r\n") {
		return nil, fmt.Errorf("invalid SMTP_FROM address")
	}
	from.Name = n.Organization.ShortName
	var replyTo string
	if s.cfg.SMTPReplyTo != "" {
		address, err := mail.ParseAddress(s.cfg.SMTPReplyTo)
		if err != nil || strings.ContainsAny(s.cfg.SMTPReplyTo, "\r\n") {
			return nil, fmt.Errorf("invalid SMTP_REPLY_TO address")
		}
		address.Name = n.Organization.ShortName
		replyTo = "Reply-To: " + address.String() + "\r\n"
	}
	var body bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&body, "notification.html", n); err != nil {
		return nil, fmt.Errorf("failed to execute email template: %w", err)
	}
	var message bytes.Buffer
	parts := multipart.NewWriter(&message)
	fmt.Fprintf(&message, "From: %s\r\n%sTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", from.String(), replyTo, recipient.String(), mime.QEncoding.Encode("utf-8", n.Title), parts.Boundary())
	plain := fmt.Sprintf("%s\n\n%s\n\nHallo %s,\n\n%s\n\n%s:\n%s\n\n%s\n\n%s\n\nDeine Fachschaft\n\nKontakt aufnehmen: %s/contact\n", n.Organization.Name, n.Title, n.Name, n.Intro, n.Action, n.Link, n.Validity, n.Note, n.Domain)
	for _, part := range []struct{ kind, body string }{{"text/plain", plain}, {"text/html", body.String()}} {
		header := textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=UTF-8"}, "Content-Transfer-Encoding": {"quoted-printable"}}
		out, err := parts.CreatePart(header)
		if err != nil {
			return nil, err
		}
		writer := quotedprintable.NewWriter(out)
		if _, err := writer.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	return message.Bytes(), nil
}

// Bound the complete SMTP exchange so unavailable mail cannot block shutdown.
func send(addr, host string, auth smtp.Auth, from, to string, message []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	return sendMessage(conn, host, auth, from, to, message)
}

func sendMessage(conn net.Conn, host string, auth smtp.Auth, from, to string, message []byte) error {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	} else if peer, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || !peer.IP.IsLoopback() {
		return fmt.Errorf("SMTP server must support STARTTLS outside loopback")
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
