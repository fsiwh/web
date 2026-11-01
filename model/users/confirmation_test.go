package users

import (
	"context"
	"testing"
)

func TestFailedConfirmationResendPreservesPreviousLink(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES (42,'person@students.example.invalid','Person','unchanged','user',1,0)`); err != nil {
		t.Fatal(err)
	}
	if queued, err := s.ResendConfirmation(ctx, 42); err != nil || !queued {
		t.Fatal("initial confirmation failed", err)
	}
	previous := queuedToken(t, db, "verify")
	if _, err := db.Exec(`CREATE TRIGGER fail_confirmation BEFORE INSERT ON mail_outbox BEGIN SELECT RAISE(FAIL,'forced outbox failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResendConfirmation(ctx, 42); err == nil {
		t.Fatal("failed mail insertion reported success")
	}
	if queuedToken(t, db, "verify") != previous {
		t.Fatal("failed resend replaced the pending email")
	}
	if err := s.ConsumeToken(ctx, previous, "verify", ""); err != nil {
		t.Fatal("failed resend invalidated the previous confirmation link", err)
	}
	if queued, err := s.ResendConfirmation(ctx, 42); err != nil || queued {
		t.Fatal("verified account received another confirmation")
	}
}
