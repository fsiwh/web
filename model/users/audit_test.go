package users

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
)

func TestAccountAuditSanitizesUntrustedEmailDomain(t *testing.T) {
	for _, separator := range []string{"", "\r", "\n", "\r\n"} {
		t.Run(separator, func(t *testing.T) {
			_, db := testService(t)
			var logs bytes.Buffer
			ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
			ctx = audit.WithRecorder(ctx, audit.NewStore(db))
			email := "private-local-part@EXAMPLE.ORG" + separator + "forged-entry"
			accountEvent(ctx, "account.login", "account_not_found", 0, email)
			var entry struct {
				Event  string `json:"event"`
				Domain string `json:"email_domain"`
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal("expected one valid audit entry", err)
			}
			if entry.Event != "account.login" || entry.Domain != "example.orgforged-entry" {
				t.Fatalf("unsafe or missing audit details: %+v", entry)
			}
			var details string
			if err := db.QueryRow(`SELECT details FROM audit_events WHERE event='account.login'`).Scan(&details); err != nil {
				t.Fatal(err)
			}
			var stored map[string]string
			if err := json.Unmarshal([]byte(details), &stored); err != nil {
				t.Fatal(err)
			}
			if stored["email_domain"] != entry.Domain {
				t.Fatalf("persisted audit domain differs: %q", stored["email_domain"])
			}
			if strings.Contains(logs.String()+details, "private-local-part") {
				t.Fatal("audit exposed the email local part")
			}
		})
	}
}

func TestSessionExpiryAuditDoesNotExposeSessionCookie(t *testing.T) {
	s, db := testService(t)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(42,'person@students.example.invalid','Person','unused','user',1,1)`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	session, err := s.CreateSession(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE web_sessions SET expires_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Session(ctx, session.ID)
	if err != nil || loaded != nil {
		t.Fatal("expired session was not rejected", err)
	}
	if !strings.Contains(logs.String(), `"event":"session.expired"`) || !strings.Contains(logs.String(), `"user_id":42`) {
		t.Fatal("missing attributable session expiry audit")
	}
	if strings.Contains(logs.String(), session.ID) || strings.Contains(logs.String(), session.CSRF) {
		t.Fatal("session audit leaked cookie or CSRF secret")
	}
}

func TestRegistrationRollbackDoesNotEmitSuccessAudit(t *testing.T) {
	s, db := testService(t)
	if _, err := db.Exec(`CREATE TRIGGER fail_registration BEFORE INSERT ON mail_outbox BEGIN SELECT RAISE(FAIL,'outbox unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	if err := s.Register(ctx, "Student", "person@students.example.invalid", "test password", "", "", ""); err == nil {
		t.Fatal("registration unexpectedly committed")
	}
	if strings.Contains(logs.String(), "verification_queued") {
		t.Fatal("rolled-back account creation emitted success audit")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed registration persisted account", err)
	}
}

func TestBackgroundSessionExpiryAuditPreservesAccountAttribution(t *testing.T) {
	s, db := testService(t)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(42,'person@students.example.invalid','Person','unused','user',1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []int64{0, 42} {
		if _, err := s.CreateSession(context.Background(), userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE web_sessions SET expires_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	if err := s.PurgeExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"user_id":42`) || strings.Count(logs.String(), `"event":"session.expired"`) != 2 {
		t.Fatal("background expiry lost account attribution")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM web_sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired sessions retained", err)
	}
}

func TestRejectedTransactionalAccountActionsRemainAuditable(t *testing.T) {
	s, db := testService(t)
	var logs bytes.Buffer
	ctx := audit.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx = audit.WithRecorder(ctx, audit.NewStore(db))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.Register(ctx, "Student", "student@students.example.invalid", "test password", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(ctx, "Student", "student@students.example.invalid", "test password", "", "", ""); err != ErrEmailExists {
		t.Fatal("duplicate registration was not rejected", err)
	}
	token := queuedToken(t, db, "verify")
	if err := s.ConsumeToken(ctx, token, "verify", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeToken(ctx, token, "verify", ""); err != ErrToken {
		t.Fatal("reused token was not rejected", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE outcome IN ('email_exists','invalid_or_expired_link')`).Scan(&count); err != nil || count != 2 {
		t.Fatal("rejected account actions did not persist audit events", err)
	}
	if strings.Contains(logs.String(), "audit persistence failed") {
		t.Fatal("audit writer competed with an unfinished account transaction")
	}
}
