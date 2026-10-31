package users

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func preferenceUser(t *testing.T, db *sql.DB) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("current password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,name,email,password,role,active,verified) VALUES(42,'Original Name','old@studmail.w-hs.de',?,'user',1,1)`, string(hash)); err != nil {
		t.Fatal(err)
	}
}

func TestPreferencesEmailConfirmationLifecycle(t *testing.T) {
	s, db := testService(t)
	preferenceUser(t, db)
	ctx := context.Background()
	if err := s.ChangeName(ctx, 42, "  Neuer Name  "); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestEmailChange(ctx, 42, "new@studmail.w-hs.de", "wrong password"); !errors.Is(err, ErrCurrentPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := s.RequestEmailChange(ctx, 42, " NEW@studmail.w-hs.de ", "current password"); err != nil {
		t.Fatal(err)
	}
	first := queuedToken(t, db, "verify")
	p, err := s.Preferences(ctx, 42)
	if err != nil || p.Name != "Neuer Name" || p.Email != "old@studmail.w-hs.de" || p.PendingEmail != "new@studmail.w-hs.de" {
		t.Fatalf("pending preferences: %+v %v", p, err)
	}
	if _, err := s.Login(ctx, p.Email, "current password"); err != nil {
		t.Fatal("request changed login prematurely", err)
	}
	if err := s.RequestEmailChange(ctx, 42, "replacement@studmail.w-hs.de", "current password"); err != nil {
		t.Fatal(err)
	}
	latest := queuedToken(t, db, "verify")
	if err := s.ConsumeToken(ctx, first, "verify", ""); !errors.Is(err, ErrToken) {
		t.Fatal("superseded link accepted", err)
	}
	if err := s.RequestReset(ctx, "old@studmail.w-hs.de"); err != nil {
		t.Fatal(err)
	}
	reset := queuedToken(t, db, "reset")
	if err := s.ConsumeToken(ctx, latest, "verify", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeToken(ctx, latest, "verify", ""); !errors.Is(err, ErrToken) {
		t.Fatal("confirmation was not single-use", err)
	}
	p, err = s.Preferences(ctx, 42)
	if err != nil || p.Email != "replacement@studmail.w-hs.de" || p.PendingEmail != "" {
		t.Fatalf("confirmed preferences: %+v %v", p, err)
	}
	if _, err := s.Login(ctx, p.Email, "current password"); err != nil {
		t.Fatal("new login failed", err)
	}
	if _, err := s.Login(ctx, "old@studmail.w-hs.de", "current password"); !errors.Is(err, ErrCredentials) {
		t.Fatal("old address still works", err)
	}
	if err := s.ConsumeToken(ctx, reset, "reset", "replacement password"); !errors.Is(err, ErrToken) {
		t.Fatal("old-address reset link survived email change", err)
	}
	var recipient string
	if err := db.QueryRow(`SELECT recipient FROM account_change_mail WHERE kind='email_changed'`).Scan(&recipient); err != nil || recipient != "old@studmail.w-hs.de" {
		t.Fatal("old mailbox did not receive notification", recipient, err)
	}
}

func TestEmailChangeRejectsExpiredAndConflictingLinks(t *testing.T) {
	for _, state := range []string{"expired", "taken", "identity changed", "inactive"} {
		t.Run(state, func(t *testing.T) {
			s, db := testService(t)
			preferenceUser(t, db)
			ctx := context.Background()
			if err := s.RequestEmailChange(ctx, 42, "new@studmail.w-hs.de", "current password"); err != nil {
				t.Fatal(err)
			}
			value := queuedToken(t, db, "verify")
			statement := map[string]string{
				"expired":          `UPDATE user_tokens SET expires_at='2000-01-01T00:00:00Z'`,
				"taken":            `INSERT INTO users(name,email,password,role,active,verified) VALUES('Other','new@studmail.w-hs.de','!','user',1,1)`,
				"identity changed": `UPDATE users SET email='admin-change@studmail.w-hs.de' WHERE id=42`,
				"inactive":         `UPDATE users SET active=0 WHERE id=42`,
			}[state]
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			want := ErrToken
			if state == "taken" {
				want = ErrEmailExists
			}
			if err := s.ConsumeToken(ctx, value, "verify", ""); !errors.Is(err, want) {
				t.Fatalf("confirmation: %v, want %v", err, want)
			}
			var address string
			if err := db.QueryRow(`SELECT email FROM users WHERE id=42`).Scan(&address); err != nil || address == "new@studmail.w-hs.de" {
				t.Fatal("rejected link changed the address", address, err)
			}
		})
	}
}

func TestEmailChangeFailedResendIsAtomic(t *testing.T) {
	s, db := testService(t)
	preferenceUser(t, db)
	ctx := context.Background()
	if err := s.RequestEmailChange(ctx, 42, "first@studmail.w-hs.de", "current password"); err != nil {
		t.Fatal(err)
	}
	value := queuedToken(t, db, "verify")
	if _, err := db.Exec(`CREATE TRIGGER fail_mail BEFORE INSERT ON mail_outbox BEGIN SELECT RAISE(ABORT,'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestEmailChange(ctx, 42, "second@studmail.w-hs.de", "current password"); err == nil {
		t.Fatal("failed outbox write reported success")
	}
	if err := s.ConsumeToken(ctx, value, "verify", ""); err != nil {
		t.Fatal("failed resend invalidated original link", err)
	}
	p, err := s.Preferences(ctx, 42)
	if err != nil || p.Email != "first@studmail.w-hs.de" {
		t.Fatalf("failed resend changed destination: %+v %v", p, err)
	}
}

func TestPasswordChangeRevokesOtherSessionsAndLinks(t *testing.T) {
	s, db := testService(t)
	preferenceUser(t, db)
	ctx := context.Background()
	current, err := s.CreateSession(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateLoginSession(ctx, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestEmailChange(ctx, 42, "new@studmail.w-hs.de", "current password"); err != nil {
		t.Fatal(err)
	}
	emailLink := queuedToken(t, db, "verify")
	if err := s.RequestReset(ctx, "old@studmail.w-hs.de"); err != nil {
		t.Fatal(err)
	}
	resetLink := queuedToken(t, db, "reset")
	if err := s.ChangePassword(ctx, 42, current.ID, "wrong", "new password"); !errors.Is(err, ErrCurrentPassword) {
		t.Fatal("wrong current password accepted", err)
	}
	if err := s.ChangePassword(ctx, 42, current.ID, "current password", "new password"); err != nil {
		t.Fatal(err)
	}
	if loaded, err := s.Session(ctx, current.ID); err != nil || loaded == nil {
		t.Fatal("current session lost", err)
	}
	if loaded, err := s.Session(ctx, other.ID); err != nil || loaded != nil {
		t.Fatal("other session survived", err)
	}
	if _, err := s.Login(ctx, "old@studmail.w-hs.de", "current password"); !errors.Is(err, ErrCredentials) {
		t.Fatal("old password survived", err)
	}
	if _, err := s.Login(ctx, "old@studmail.w-hs.de", "new password"); err != nil {
		t.Fatal("new password failed", err)
	}
	for kind, value := range map[string]string{"verify": emailLink, "reset": resetLink} {
		if err := s.ConsumeToken(ctx, value, kind, "another password"); !errors.Is(err, ErrToken) {
			t.Fatal("password change retained account link", kind, err)
		}
	}
	mailer := &queuedMailer{failTo: "old@studmail.w-hs.de"}
	if err := s.DeliverMail(ctx, mailer); err == nil {
		t.Fatal("notification delivery failure not reported")
	}
	mailer.failTo = ""
	if err := s.DeliverMail(ctx, mailer); err != nil || len(mailer.delivered) != 1 {
		t.Fatal("notification did not survive SMTP failure", mailer.delivered, err)
	}
}
