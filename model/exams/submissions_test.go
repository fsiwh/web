package exams

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func reviewService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	s, db := examsService(t)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES (2,'reviewer@council.example.invalid','Reviewer','unused','editor',1,1),(3,'reader@council.example.invalid','Reader','unused','user',1,1)`); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestDraftPrivacySubmissionAndConfirmation(t *testing.T) {
	s, db := reviewService(t)
	ctx := context.Background()
	input := submission(t, db)
	result, err := s.SaveDraft(ctx, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []int64{2, 3} {
		if _, err := s.Open(ctx, userID, result.IDs[0]); !errors.Is(err, ErrNotFound) {
			t.Fatal("draft PDF exposed to another account")
		}
		if _, err := s.Submission(ctx, userID, result.SubmissionID, false); !errors.Is(err, ErrNotFound) {
			t.Fatal("draft submission exposed to another account")
		}
	}
	if _, err := s.Submission(ctx, 2, result.SubmissionID, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("reviewer accessed an owner-only draft")
	}
	if err := s.Submit(ctx, 3, result.SubmissionID); !errors.Is(err, ErrNotFound) {
		t.Fatal("another account submitted the draft")
	}
	input.SubmissionID = result.SubmissionID
	input.Documents = nil
	input.Comment = "Geprüfte Angaben"
	if _, err := s.SaveDraft(ctx, 1, input); err != nil {
		t.Fatal(err)
	}
	current, err := s.Submission(ctx, 1, result.SubmissionID, false)
	if err != nil || len(current.Documents) != 1 || current.Documents[0].ID != result.IDs[0] || current.Comment != input.Comment {
		t.Fatal("editing metadata did not retain PDFs")
	}
	if err := s.Submit(ctx, 1, result.SubmissionID); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(ctx, 1, result.SubmissionID); err != nil {
		t.Fatal("submission replay was not idempotent")
	}
	if _, err := s.SaveDraft(ctx, 1, input); !errors.Is(err, ErrConflict) {
		t.Fatal("submitted submission remained editable")
	}
	if err := s.Review(ctx, 1, result.SubmissionID, true); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary uploader approved a submission")
	}
	object, err := s.Open(ctx, 2, result.IDs[0])
	if err != nil {
		t.Fatal("reviewer cannot inspect pending PDF")
	}
	object.Close()
	if _, err := s.Open(ctx, 3, result.IDs[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("pending PDF exposed in public exams")
	}
	catalog, err := s.Catalog(ctx)
	if err != nil || len(catalog.FindExams(Filter{})) != 3 {
		t.Fatal("pending files appeared in public catalogue")
	}
	if err := s.Review(ctx, 2, result.SubmissionID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Review(ctx, 2, result.SubmissionID, true); err != nil {
		t.Fatal("confirmation replay was not idempotent")
	}
	catalog, err = s.Catalog(ctx)
	if err != nil || len(catalog.FindExams(Filter{})) != 4 {
		t.Fatal("confirmed files not published")
	}
	object, err = s.Open(ctx, 3, result.IDs[0])
	if err != nil {
		t.Fatal("confirmed PDF not available to authenticated reader")
	}
	object.Close()
	var reviewer int64
	if err := db.QueryRow(`SELECT reviewed_by FROM archive_requests WHERE id=? AND status='approved' AND reviewed_at IS NOT NULL`, testInternalID(t, db, "submissions", result.SubmissionID)).Scan(&reviewer); err != nil || reviewer != 2 {
		t.Fatal("confirmation actor or status not recorded")
	}
}

func TestPendingRevisionRetainsApprovedFileAndRejectsStaleApproval(t *testing.T) {
	s, db := reviewService(t)
	ctx := context.Background()
	input := submission(t, db)
	input.RevisionOf = testPublicID(t, db, "documents", "101")
	input.Documents = input.Documents[:1]
	first, err := s.SaveDraft(ctx, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveDraft(ctx, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []UploadResult{first, second} {
		if err := s.Submit(ctx, 1, result.SubmissionID); err != nil {
			t.Fatal(err)
		}
	}
	var latest int
	if err := db.QueryRow(`SELECT is_latest FROM archive WHERE id=101`).Scan(&latest); err != nil || latest != 1 {
		t.Fatal("pending revision displaced confirmed file")
	}
	if err := s.Review(ctx, 2, first.SubmissionID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Review(ctx, 2, second.SubmissionID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision replaced a more recently confirmed file")
	}
	if err := s.Review(ctx, 2, second.SubmissionID, false); err != nil {
		t.Fatal(err)
	}
	current, err := s.Submission(ctx, 1, second.SubmissionID, false)
	if err != nil || current.Status != Rejected {
		t.Fatal("rejection status not visible to owner")
	}
	if _, err := s.Open(ctx, 3, second.IDs[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("rejected PDF exposed")
	}
	if err := db.QueryRow(`SELECT is_latest FROM archive WHERE id=?`, testInternalID(t, db, "documents", first.IDs[0])).Scan(&latest); err != nil || latest != 1 {
		t.Fatal("rejection displaced the approved revision")
	}
}

func TestReviewFailureRollsBackPublication(t *testing.T) {
	s, db := reviewService(t)
	ctx := context.Background()
	input := submission(t, db)
	input.RevisionOf = testPublicID(t, db, "documents", "101")
	input.Documents = input.Documents[:1]
	result, err := s.SaveDraft(ctx, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(ctx, 1, result.SubmissionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_review BEFORE UPDATE OF status ON archive_requests WHEN NEW.status='approved' BEGIN SELECT RAISE(FAIL,'forced review failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Review(ctx, 2, result.SubmissionID, true); err == nil {
		t.Fatal("failed review reported success")
	}
	var latest int
	if err := db.QueryRow(`SELECT is_latest FROM archive WHERE id=101`).Scan(&latest); err != nil || latest != 1 {
		t.Fatal("failed confirmation removed previous revision")
	}
	current, err := s.Submission(ctx, 1, result.SubmissionID, false)
	if err != nil || current.Status != Pending {
		t.Fatal("failed confirmation changed submission status")
	}
}
