package email

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/config"
	"github.com/fachschaftinformatik/web/model/organization"
)

func TestSubmissionEmailContainsAuthenticatedReviewLink(t *testing.T) {
	testSubmissionEmail(t, func(sender *Sender) error {
		return sender.SendSubmissionEmail("reviewer@example.org", "Reviewer", "AbCdEfGhIjK")
	}, "/admin/submissions/AbCdEfGhIjK", "deaktivieren")
}

func TestSubmissionDecisionEmails(t *testing.T) {
	for _, approved := range []bool{true, false} {
		label := "abgelehnt"
		if approved {
			label = "freigegeben"
		}
		t.Run(label, func(t *testing.T) {
			testSubmissionEmail(t, func(sender *Sender) error {
				return sender.SendSubmissionDecisionEmail("submitter@example.org", "Submitter", "AbCdEfGhIjK", approved)
			}, "/exams/submissions/AbCdEfGhIjK", label)
		})
	}
}

func testSubmissionEmail(t *testing.T, send func(*Sender) error, path, content string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- nil
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 localhost ESMTP\r\n")
		var message []byte
		defer func() { received <- message }()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "DATA"):
				fmt.Fprint(conn, "354 Send message\r\n")
				message, _ = textproto.NewReader(reader).ReadDotBytes()
				fmt.Fprint(conn, "250 Accepted\r\n")
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprint(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 localhost\r\n")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	sender := NewSender(&config.Config{Domain: "https://fsi-wh.de", SMTPHost: host, SMTPPort: port, SMTPFrom: "web@example.org"}, nil)
	if err := send(sender); err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(bytes.NewReader(<-received))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	for range 2 {
		part, err := parts.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "https://fsi-wh.de"+path) || !strings.Contains(string(body), "Einsendung") || !strings.Contains(string(body), content) || strings.Contains(string(body), "token=") {
			t.Fatal("submission notification lost its link or expected content", string(body))
		}
	}
}

func TestRemoteSMTPWithoutTLSDoesNotReceiveMessage(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan string, 1)
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		fmt.Fprint(server, "220 mail.example.org ESMTP\r\n")
		line, _ := reader.ReadString('\n')
		if !strings.HasPrefix(line, "EHLO ") {
			done <- line
			return
		}
		fmt.Fprint(server, "250 mail.example.org\r\n")
		line, _ = reader.ReadString('\n')
		done <- line
	}()
	err := sendMessage(client, "mail.example.org", nil, "web@example.org", "user@example.org", []byte("secret reset link"))
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("unencrypted remote mail accepted: %v", err)
	}
	if command := <-done; command != "" {
		t.Fatalf("sent a command after discovering TLS was unavailable: %q", command)
	}
}

func TestNotificationMIMEPreservesUnicodeAndActionLinks(t *testing.T) {
	sender := NewSender(&config.Config{SMTPFrom: "web@example.org"}, nil)
	n := notification{
		Organization: organization.Details{Name: "Example", ShortName: "Example Council", Institution: "Example University"},
		Name:         "Jörg <script>alert(1)</script>", Domain: "https://example.org",
		Title: "E-Mail-Adresse bestätigen", Intro: "Bestätige dein Konto.",
		Action: "E-Mail-Adresse bestätigen", Link: "https://example.org/email/confirm?token=" + strings.Repeat("a", 64) + "&source=email",
		Validity: "Der Link ist 24 Stunden gültig.", Note: "Deine Fachschaft.",
	}
	encoded, err := sender.message("person@example.org", n)
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || subject != n.Title {
		t.Fatal("subject did not preserve German characters")
	}
	kind, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || kind != "multipart/alternative" {
		t.Fatal("missing alternative email representations")
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	for _, expected := range []string{"text/plain", "text/html"} {
		part, err := parts.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		kind, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil || kind != expected || params["charset"] != "UTF-8" {
			t.Fatal("invalid email part")
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, n.Title) || !strings.Contains(text, n.Validity) {
			t.Fatal("Unicode message content did not survive transport encoding")
		}
		if expected == "text/plain" {
			if !strings.Contains(text, n.Link) || !strings.Contains(text, n.Name) {
				t.Fatal("plain-text message lost the action link or recipient")
			}
		} else {
			if strings.Contains(text, "<script>") || !strings.Contains(text, "&lt;script&gt;") {
				t.Fatal("recipient name was not escaped")
			}
			if !strings.Contains(text, `href="`+strings.ReplaceAll(n.Link, "&", "&amp;")+`"`) {
				t.Fatal("HTML message lost the action link")
			}
		}
	}
	if _, err := parts.NextPart(); err != io.EOF {
		t.Fatal("unexpected email part")
	}
}

func TestNotificationSenderAndReplyAddress(t *testing.T) {
	for _, reply := range []string{"", "team@fsi-wh.de", "invalid", "team@fsi-wh.de\r\nBcc: other@example.org"} {
		t.Run(reply, func(t *testing.T) {
			sender := NewSender(&config.Config{SMTPFrom: "portal@fsi-wh.de", SMTPReplyTo: reply}, nil)
			raw, err := sender.message("user@example.org", notification{Organization: organization.Details{ShortName: "fsInformatik"}})
			if reply != "" && reply != "team@fsi-wh.de" {
				if err == nil {
					t.Fatal("invalid reply address accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			message, err := mail.ReadMessage(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			from, err := mail.ParseAddress(message.Header.Get("From"))
			if err != nil || from.Address != "portal@fsi-wh.de" || from.Name != "fsInformatik" {
				t.Fatal("sender identity missing", err)
			}
			if reply == "" {
				if message.Header.Get("Reply-To") != "" {
					t.Fatal("unexpected reply override")
				}
			} else {
				address, err := mail.ParseAddress(message.Header.Get("Reply-To"))
				if err != nil || address.Address != reply || address.Name != "fsInformatik" {
					t.Fatal("reply routing missing", err)
				}
			}
		})
	}
}

func TestNotificationRejectsHeaderInjection(t *testing.T) {
	sender := NewSender(&config.Config{SMTPFrom: "web@example.org"}, nil)
	for _, recipient := range []string{"invalid", "user@example.org\r\nBcc: attacker@example.org", "user@example.org\n\nInjected body", "user@example.org, attacker@example.org"} {
		if _, err := sender.message(recipient, notification{Title: "Feedback"}); err == nil {
			t.Errorf("accepted invalid recipient %q", recipient)
		}
	}
	if _, err := sender.message("user@example.org", notification{Title: "Feedback\r\nBcc: attacker@example.org"}); err == nil {
		t.Fatal("accepted injected subject")
	}
}

func TestNotificationFeedbackTextCannotInjectMIME(t *testing.T) {
	sender := NewSender(&config.Config{SMTPFrom: "web@example.org"}, nil)
	payload := "First line\r\nBcc: attacker@example.org\r\n\r\n.\r\nContent-Type: text/html\r\n<script>alert(1)</script>\nGrüße"
	raw, err := sender.message("user@example.org", notification{Title: "Feedback", Intro: payload, Validity: payload, Note: payload})
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("Bcc") != "" || message.Header.Get("Subject") != "Feedback" {
		t.Fatal("feedback text escaped into message headers")
	}
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	for _, kind := range []string{"text/plain", "text/html"} {
		part, err := parts.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(part.Header.Get("Content-Type"), kind) {
			t.Fatal("feedback text changed MIME structure")
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("Grüße")) || !bytes.Contains(body, []byte("Bcc: attacker@example.org")) {
			t.Fatal("feedback text was not preserved in body")
		}
		if kind == "text/html" && (bytes.Contains(body, []byte("<script>")) || !bytes.Contains(body, []byte("&lt;script&gt;"))) {
			t.Fatal("feedback HTML was not escaped")
		}
	}
	if _, err := parts.NextPart(); err != io.EOF {
		t.Fatal("feedback text injected a MIME part")
	}
}
