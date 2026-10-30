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
	tpl, err := template.ParseFS(templateFS, "templates/*.html")
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
		Intro:    "Bestätige deine E-Mail-Adresse, um dein Benutzerkonto bei " + s.organization.Current().ShortName + " zu nutzen.",
		Action:   "E-Mail-Adresse bestätigen",
		Validity: "Der Link ist 24 Stunden gültig.",
		Note:     "Wenn du dich nicht registriert hast, kannst du diese E-Mail ignorieren.",
	}
	if target, err := url.Parse(link); err == nil && target.Path == "/invitation/accept" {
		n.Title = "Einladung: " + s.organization.Current().ShortName
		n.Intro = "Du wurdest als Mitglied der Fachschaft eingeladen. Lege dein Passwort fest und bestätige deine E-Mail-Adresse, um dein Benutzerkonto zu nutzen."
		n.Action = "Einladung annehmen"
		n.Note = "Bei Fragen zu deiner Einladung wende dich an die Fachschaft."
	}
	return s.notify(toEmail, n)
}

func (s *Sender) SendPasswordResetEmail(toEmail, name, link string) error {
	return s.notify(toEmail, notification{
		Name: name, Link: link,
		Title:    "Passwort zurücksetzen",
		Intro:    "Für dein Benutzerkonto wurde ein neues Passwort angefordert. Über den folgenden Link kannst du ein neues Passwort festlegen.",
		Action:   "Passwort zurücksetzen",
		Validity: "Der Link ist eine Stunde gültig.",
		Note:     "Wenn du kein neues Passwort angefordert hast, kannst du diese E-Mail ignorieren. Dein bisheriges Passwort bleibt gültig.",
	})
}

func (s *Sender) SendSubmissionEmail(toEmail, name, submissionID string) error {
	return s.notify(toEmail, notification{
		Name: name, Link: s.cfg.Domain + urls.ReviewSubmission(submissionID),
		Title:  "Neue Einsendung für die Altklausuren",
		Intro:  "Eine neue Einsendung wartet auf deine Prüfung. Melde dich an, um die Dokumente und Angaben zu prüfen.",
		Action: "Einsendung prüfen",
		Note:   "Du erhältst diese E-Mail, weil du Benachrichtigungen über neue Einsendungen aktiviert hast. Du kannst sie in den Einstellungen für Altklausuren deaktivieren.",
	})
}

func (s *Sender) SendSubmissionDecisionEmail(toEmail, name, submissionID string, approved bool) error {
	n := notification{
		Name: name, Link: s.cfg.Domain + urls.Submission(submissionID),
		Title:  "Deine Einsendung wurde abgelehnt",
		Intro:  "Deine Einsendung für die Altklausuren wurde geprüft und abgelehnt. Du kannst den aktuellen Status nach der Anmeldung ansehen.",
		Action: "Einsendung ansehen",
		Note:   "Bei Fragen zu dieser Entscheidung wende dich an die Fachschaft.",
	}
	if approved {
		n.Title = "Deine Einsendung wurde freigegeben"
		n.Intro = "Deine Einsendung für die Altklausuren wurde geprüft und freigegeben. Vielen Dank für deinen Beitrag! Du kannst den aktuellen Status nach der Anmeldung ansehen."
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
	if s.organization != nil {
		n.Organization = s.organization.Current()
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
	fmt.Fprintf(&message, "From: %s\r\n%sTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", from.String(), replyTo, to, mime.QEncoding.Encode("utf-8", n.Title), parts.Boundary())
	plain := fmt.Sprintf("%s\n\n%s\n\nHallo %s,\n\n%s\n\n%s:\n%s\n\n%s\n\n%s\n\nDeine %s\n\n%s · %s\nFachschaft kontaktieren: %s/contact\n", n.Organization.Name, n.Title, n.Name, n.Intro, n.Action, n.Link, n.Validity, n.Note, n.Organization.ShortName, n.Organization.ShortName, n.Organization.Institution, n.Domain)
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
