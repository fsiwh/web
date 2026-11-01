package exams

import (
	"context"
	"errors"
	"testing"
)

type submissionMailRecorder struct {
	to, name, id string
	calls        int
	err          error
}

func (m *submissionMailRecorder) SendSubmissionEmail(to, name, id string) error {
	m.to, m.name, m.id = to, name, id
	m.calls++
	return m.err
}

func TestSubmissionMailOptInAndRetry(t *testing.T) {
	s, db := reviewService(t)
	ctx := context.Background()
	if enabled, permitted, err := s.SubmissionMailPreference(ctx, 2); err != nil || enabled || !permitted {
		t.Fatalf("reviewer default preference: %v %v %v", enabled, permitted, err)
	}
	if err := s.SaveSubmissionMailPreference(ctx, 3, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary account subscribed to private submissions: %v", err)
	}
	if err := s.SaveSubmissionMailPreference(ctx, 2, true); err != nil {
		t.Fatal(err)
	}
	// Even a stale subscription cannot bypass reviewer authorization.
	if _, err := db.Exec(`INSERT INTO submission_mail_subscriptions(user_id) VALUES(3)`); err != nil {
		t.Fatal(err)
	}
	draft, err := s.SaveDraft(ctx, 1, submission(t, db))
	if err != nil {
		t.Fatal(err)
	}
	mailer := &submissionMailRecorder{err: errors.New("SMTP unavailable")}
	if err := s.DeliverSubmissionMail(ctx, mailer); err != nil || mailer.calls != 0 {
		t.Fatal("draft sent a notification", err)
	}
	for range 2 {
		if err := s.Submit(ctx, 1, draft.SubmissionID); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM submission_mail_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("submission replay or recipient filtering queued %d messages: %v", count, err)
	}
	if err := s.DeliverSubmissionMail(ctx, mailer); err == nil || mailer.calls != 1 {
		t.Fatal("SMTP failure was not returned")
	}
	if _, err := db.Exec(`UPDATE users SET email='reviewer@portal.example.invalid' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	mailer.err = nil
	if err := s.DeliverSubmissionMail(ctx, mailer); err != nil {
		t.Fatal(err)
	}
	if mailer.calls != 2 || mailer.to != "reviewer@portal.example.invalid" || mailer.name != "Reviewer" || mailer.id != draft.SubmissionID {
		t.Fatalf("retry did not use the current recipient and stable public submission ID: %+v", mailer)
	}
	if err := s.DeliverSubmissionMail(ctx, mailer); err != nil || mailer.calls != 2 {
		t.Fatal("delivered message was repeated", err)
	}
}

func TestSubmissionAndNotificationQueueCommitTogether(t *testing.T) {
	s, db := reviewService(t)
	ctx := context.Background()
	if err := s.SaveSubmissionMailPreference(ctx, 2, true); err != nil {
		t.Fatal(err)
	}
	draft, err := s.SaveDraft(ctx, 1, submission(t, db))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_submission_mail BEFORE INSERT ON submission_mail_outbox BEGIN SELECT RAISE(FAIL,'outbox unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(ctx, 1, draft.SubmissionID); err == nil {
		t.Fatal("submission succeeded without its notification")
	}
	current, err := s.Submission(ctx, 1, draft.SubmissionID, false)
	if err != nil || current.Status != Draft || current.SubmittedAt != nil {
		t.Fatal("failed notification did not roll back submission", current.Status, err)
	}
}

func TestSubmissionMailDropsNoLongerEligibleMessages(t *testing.T) {
	for _, change := range []string{
		`UPDATE users SET role='user' WHERE id=2`,
		`UPDATE users SET active=0 WHERE id=2`,
		`UPDATE users SET verified=0 WHERE id=2`,
		`UPDATE users SET deleted_at='2026-10-18T00:00:00Z' WHERE id=2`,
		`UPDATE archive_requests SET status='approved'`,
		"unsubscribe",
	} {
		t.Run(change, func(t *testing.T) {
			s, db := reviewService(t)
			ctx := context.Background()
			if err := s.SaveSubmissionMailPreference(ctx, 2, true); err != nil {
				t.Fatal(err)
			}
			draft, err := s.SaveDraft(ctx, 1, submission(t, db))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Submit(ctx, 1, draft.SubmissionID); err != nil {
				t.Fatal(err)
			}
			if change == "unsubscribe" {
				err = s.SaveSubmissionMailPreference(ctx, 2, false)
			} else {
				_, err = db.Exec(change)
			}
			if err != nil {
				t.Fatal(err)
			}
			mailer := &submissionMailRecorder{}
			if err := s.DeliverSubmissionMail(ctx, mailer); err != nil || mailer.calls != 0 {
				t.Fatal("ineligible recipient received a notification", err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM submission_mail_outbox`).Scan(&count); err != nil || count != 0 {
				t.Fatal("ineligible message was not removed", err)
			}
		})
	}
}
