package exams

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
)

type Status string

const (
	Draft    Status = "draft"
	Pending  Status = "pending"
	Approved Status = "approved"
	Rejected Status = "rejected"
)

var ErrConflict = errors.New("Die Einreichung wurde bereits geändert. Lade die Seite neu und prüfe den aktuellen Stand.")

type Submission struct {
	ID, Name, Title, Program, ModuleID, Version, Date, Comment, RevisionOf string
	UserID                                                                 int64
	UserPublicID                                                           string
	Status                                                                 Status
	CreatedAt                                                              string
	SubmittedAt, ReviewedAt                                                *string
	Documents                                                              []Exam
	Count                                                                  int
	ModuleCount                                                            int
	Assignments                                                            []Assignment
	SharedAssignments                                                      bool
	EditToken                                                              string
}

func accountRole(ctx context.Context, db database.DBTX, userID int64) (string, error) {
	var role string
	err := db.QueryRowContext(ctx, `SELECT role FROM users WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL`, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrForbidden
	}
	return role, err
}

func canReview(role string) bool { return role == "admin" || role == "editor" }

func (s *Service) Submission(ctx context.Context, userID int64, value string, reviewer bool) (Submission, error) {
	value, err := resolveSubmission(ctx, s.db, value)
	if err != nil {
		return Submission{}, err
	}
	role, err := accountRole(ctx, s.db, userID)
	if err != nil {
		return Submission{}, err
	}
	if reviewer && !canReview(role) {
		return Submission{}, ErrForbidden
	}
	var result Submission
	var base *int64
	err = s.db.QueryRowContext(ctx, `SELECT r.id,r.user_id,r.status,r.revision_of,r.created_at,r.submitted_at,r.reviewed_at,u.name FROM archive_requests r JOIN users u ON u.id=r.user_id WHERE r.id=?`, value).Scan(&result.ID, &result.UserID, &result.Status, &base, &result.CreatedAt, &result.SubmittedAt, &result.ReviewedAt, &result.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, err
	}
	if (reviewer && result.Status == Draft) || (!reviewer && result.UserID != userID) {
		return Submission{}, ErrNotFound
	}
	if base != nil {
		result.RevisionOf = strconv.FormatInt(*base, 10)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.module_id,m.name,p.name,a.version,a.exam_date,COALESCE(a.comment,''),a.nbytes FROM archive a JOIN modules m ON m.id=a.module_id JOIN programs p ON p.id=m.program_id WHERE a.request_id=? AND a.deleted_at IS NULL ORDER BY a.id`, value)
	if err != nil {
		return Submission{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, moduleID int64
		var size int64
		if err := rows.Scan(&id, &moduleID, &result.Title, &result.Program, &result.Version, &result.Date, &result.Comment, &size); err != nil {
			return Submission{}, err
		}
		result.ModuleID = strconv.FormatInt(moduleID, 10)
		result.Documents = append(result.Documents, Exam{ID: strconv.FormatInt(id, 10), ModuleID: result.ModuleID, Title: result.Title, Date: result.Date, Bytes: size, Number: len(result.Documents) + 1})
	}
	if err := rows.Err(); err != nil {
		return Submission{}, err
	}
	rows.Close()
	if len(result.Documents) == 0 {
		return Submission{}, ErrNotFound
	}
	result.Count = len(result.Documents)
	result.SharedAssignments = true
	for i := range result.Documents {
		entryID, _ := strconv.ParseInt(result.Documents[i].ID, 10, 64)
		selected, err := assignments(ctx, s.db, entryID)
		if err != nil {
			return Submission{}, err
		}
		result.Documents[i].Assignments = selected
		if i == 0 {
			result.Assignments = selected
			continue
		}
		if !slices.EqualFunc(result.Assignments, selected, func(a, b Assignment) bool { return a.ModuleID == b.ModuleID && a.Version == b.Version }) {
			result.SharedAssignments = false
		}
	}
	var titles []string
	for _, a := range result.Assignments {
		if !slices.Contains(titles, a.Title) {
			titles = append(titles, a.Title)
		}
	}
	result.Title = strings.Join(titles, ", ")
	if reviewer {
		result.EditToken, err = submissionEditToken(ctx, s.db, value)
		if err != nil {
			return Submission{}, err
		}
	}
	result.ID, err = publicid.Lookup(ctx, s.db, "submissions", result.ID)
	if err != nil {
		return Submission{}, err
	}
	result.UserPublicID, err = publicid.Lookup(ctx, s.db, "users", strconv.FormatInt(result.UserID, 10))
	if err != nil {
		return Submission{}, err
	}
	if result.RevisionOf != "" {
		result.RevisionOf, err = publicid.Lookup(ctx, s.db, "documents", result.RevisionOf)
		if err != nil {
			return Submission{}, err
		}
	}
	result.ModuleID, err = publicid.Lookup(ctx, s.db, "modules", result.ModuleID)
	if err != nil {
		return Submission{}, err
	}
	for i := range result.Documents {
		d := &result.Documents[i]
		d.ID, err = publicid.Lookup(ctx, s.db, "documents", d.ID)
		if err != nil {
			return Submission{}, err
		}
		d.ModuleID, err = publicid.Lookup(ctx, s.db, "modules", d.ModuleID)
		if err != nil {
			return Submission{}, err
		}
	}
	return result, nil
}

func (s *Service) Submissions(ctx context.Context, userID int64, reviewer bool) ([]Submission, error) {
	role, err := accountRole(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	if reviewer && !canReview(role) {
		return nil, ErrForbidden
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.user_id,r.status,r.created_at,r.submitted_at,u.name,MIN(m.name),COUNT(DISTINCT a.id),COUNT(DISTINCT m.name) FROM archive_requests r JOIN users u ON u.id=r.user_id JOIN archive a ON a.request_id=r.id AND a.deleted_at IS NULL JOIN archive_placements b ON b.archive_id=a.id JOIN modules m ON m.id=b.module_id WHERE (? AND r.status!='draft') OR (NOT ? AND r.user_id=?) GROUP BY r.id ORDER BY CASE r.status WHEN 'pending' THEN 0 ELSE 1 END,r.created_at DESC,r.id DESC`, reviewer, reviewer, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Submission
	for rows.Next() {
		var current Submission
		if err := rows.Scan(&current.ID, &current.UserID, &current.Status, &current.CreatedAt, &current.SubmittedAt, &current.Name, &current.Title, &current.Count, &current.ModuleCount); err != nil {
			return nil, err
		}
		result = append(result, current)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].ID, err = publicid.Lookup(ctx, s.db, "submissions", result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].UserPublicID, err = publicid.Lookup(ctx, s.db, "users", strconv.FormatInt(result[i].UserID, 10))
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Service) Submit(ctx context.Context, userID int64, value string) error {
	publicSubmissionID := value
	value, err := resolveSubmission(ctx, s.db, value)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := accountRole(ctx, tx, userID); err != nil {
		return err
	}
	var status Status
	err = tx.QueryRowContext(ctx, `SELECT status FROM archive_requests WHERE id=? AND user_id=?`, value, userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == Pending {
		return nil
	}
	if status != Draft {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE archive_requests SET status='pending',submitted_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id=?`, value); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO submission_mail_outbox(submission_id,user_id)
		SELECT ?,u.id FROM users u JOIN submission_mail_subscriptions p ON p.user_id=u.id
		WHERE u.active=1 AND u.verified=1 AND u.deleted_at IS NULL AND u.role IN ('admin','editor')`, value); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	audit.Audit(ctx, "submission.submitted", "success", userID, slog.String("submission_id", publicSubmissionID))
	return nil
}

// Review updates the submission and public revision flags in one transaction.
func (s *Service) Review(ctx context.Context, userID int64, value string, approve bool) error {
	return s.review(ctx, userID, value, approve, nil)
}

// ReviewVersion applies a decision only to the content and moderation state that
// the reviewer inspected. An empty or stale token always conflicts.
func (s *Service) ReviewVersion(ctx context.Context, userID int64, value string, approve bool, expectedToken string) error {
	return s.review(ctx, userID, value, approve, &expectedToken)
}

func (s *Service) review(ctx context.Context, userID int64, value string, approve bool, expectedToken *string) error {
	publicSubmissionID := value
	value, err := resolveSubmission(ctx, s.db, value)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize with edits and other decisions before reading the expected state.
	if _, err := tx.ExecContext(ctx, "UPDATE archive_requests SET id=id WHERE id=?", value); err != nil {
		return err
	}
	role, err := accountRole(ctx, tx, userID)
	if err != nil {
		return err
	}
	if !canReview(role) {
		return ErrForbidden
	}
	var status Status
	var base *int64
	err = tx.QueryRowContext(ctx, `SELECT status,revision_of FROM archive_requests WHERE id=?`, value).Scan(&status, &base)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectedToken != nil {
		current, err := submissionEditToken(ctx, tx, value)
		if err != nil {
			return err
		}
		if *expectedToken == "" || current != *expectedToken {
			return ErrConflict
		}
	}
	target := Rejected
	if approve {
		target = Approved
	}
	if status == target {
		return nil
	}
	if status != Pending && status != Approved && status != Rejected {
		return ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM archive WHERE request_id=? AND deleted_at IS NULL ORDER BY id`, value)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return ErrNotFound
	}
	if approve {
		for _, id := range ids {
			entry, err := database.New(tx).GetArchiveEntry(ctx, id)
			if err != nil {
				return err
			}
			var newerHead bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM archive WHERE group_id=? AND is_latest=1 AND deleted_at IS NULL AND edit_version>?)`, entry.GroupID, entry.EditVersion).Scan(&newerHead); err != nil {
				return err
			}
			if newerHead {
				return ErrConflict
			}
			// A repeated upload can add applicability to an existing document,
			// but only after review. Record this as a new applicability revision
			// so pending revisions against the old head become stale normally.
			if base == nil && entry.GroupID == entry.ID {
				var existingID, groupID, editVersion int64
				err := tx.QueryRowContext(ctx, `SELECT a.id,a.group_id,(SELECT MAX(edit_version)+1 FROM archive h WHERE h.group_id=a.group_id) FROM archive a LEFT JOIN archive_requests r ON r.id=a.request_id
				WHERE a.id!=? AND a.checksum=? AND a.nbytes=? AND (SELECT original.user_id FROM archive original WHERE original.group_id=a.group_id ORDER BY original.edit_version,original.id LIMIT 1)=? AND a.exam_date=? AND COALESCE(a.comment,'')=COALESCE(?,'') AND a.is_latest=1 AND a.deleted_at IS NULL AND (r.id IS NULL OR r.status='approved')
				AND NOT EXISTS(SELECT 1 FROM archive_entry_aliases x WHERE x.legacy_id=a.id AND x.canonical_id!=a.id) ORDER BY a.id LIMIT 1`, entry.ID, entry.Checksum, entry.Nbytes, entry.UserID, entry.ExamDate, entry.Comment).Scan(&existingID, &groupID, &editVersion)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if err == nil {
					if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO archive_assignments(archive_id,module_id,version) SELECT ?,module_id,version FROM archive_placements WHERE archive_id=?`, id, existingID); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO archive_group_aliases(group_id,canonical_group_id) VALUES(?,?)", entry.GroupID, groupID); err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, "UPDATE archive SET group_id=?,edit_version=? WHERE id=?", groupID, editVersion, id); err != nil {
						return err
					}
					entry.GroupID = groupID
				}
			}
			if base != nil {
				latest, err := database.New(tx).GetLatestByGroupId(ctx, entry.GroupID)
				if errors.Is(err, sql.ErrNoRows) || err == nil && latest.ID != *base {
					return ErrConflict
				}
				if err != nil {
					return err
				}
			}
			var exists int
			err = tx.QueryRowContext(ctx, `SELECT 1 FROM modules m JOIN programs p ON p.id=m.program_id WHERE m.id=? AND m.deleted_at IS NULL AND p.deleted_at IS NULL`, entry.ModuleID).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			selected, err := assignments(ctx, tx, id)
			if err != nil {
				return err
			}
			if len(selected) == 0 {
				return ErrConflict
			}
			for _, a := range selected {
				moduleID, err := resolveID(ctx, tx, "modules", a.ModuleID)
				if err != nil {
					return err
				}
				var valid int
				err = tx.QueryRowContext(ctx, `SELECT 1 FROM modules m JOIN programs p ON p.id=m.program_id AND p.deleted_at IS NULL JOIN program_versions v ON v.program_id=p.id AND v.name=? WHERE m.id=? AND m.deleted_at IS NULL`, a.Version, moduleID).Scan(&valid)
				if errors.Is(err, sql.ErrNoRows) {
					return ErrConflict
				}
				if err != nil {
					return err
				}
			}
			if err := database.New(tx).ClearLatestFlagByGroupId(ctx, entry.GroupID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE archive SET is_latest=0 WHERE group_id IN (SELECT group_id FROM archive_group_aliases WHERE canonical_group_id=?)`, entry.GroupID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE archive SET is_latest=1 WHERE id=?`, id); err != nil {
				return err
			}
		}
	} else if status == Approved {
		for _, id := range ids {
			entry, err := database.New(tx).GetArchiveEntry(ctx, id)
			if err != nil {
				return err
			}
			// A decision about an older revision must not displace a newer head.
			if entry.IsLatest == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE archive SET is_latest=0 WHERE id=?`, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE archive SET is_latest=1 WHERE id=(
				SELECT a.id FROM archive a LEFT JOIN archive_requests r ON r.id=a.request_id
				WHERE a.group_id=? AND a.deleted_at IS NULL AND a.edit_version<?
				AND (r.id IS NULL OR r.status='approved') AND (a.request_id IS NULL OR a.request_id!=?)
				AND NOT EXISTS(SELECT 1 FROM archive_entry_aliases x WHERE x.legacy_id=a.id AND x.canonical_id!=a.id)
				ORDER BY a.edit_version DESC,a.id DESC LIMIT 1)`, entry.GroupID, entry.EditVersion, value); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE archive_requests SET status=?,reviewed_at=strftime('%Y-%m-%dT%H:%M:%SZ','now'),reviewed_by=? WHERE id=?`, target, userID, value); err != nil {
		return err
	}
	// Replace an undelivered decision if moderation changes again before delivery.
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO submission_decision_mail_outbox(submission_id,status) VALUES(?,?)`, value, target); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	audit.Audit(ctx, "submission.reviewed", "success", userID, slog.String("submission_id", publicSubmissionID), slog.String("previous_status", string(status)), slog.String("status", string(target)))
	return nil
}

// submissionEditToken includes moderation and publication state as well as content,
// so a stale edit cannot silently undo another editor's work or decision.
func submissionEditToken(ctx context.Context, db database.DBTX, submissionID string) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.status,COALESCE(CAST(r.revision_of AS TEXT),''),COALESCE(r.reviewed_at,''),COALESCE(CAST(r.reviewed_by AS TEXT),''),a.id,a.group_id,a.edit_version,a.is_latest,a.module_id,a.version,a.exam_date,COALESCE(a.comment,''),a.checksum,a.nbytes,a.updated_at,p.module_id,p.version
		FROM archive_requests r JOIN archive a ON a.request_id=r.id AND a.deleted_at IS NULL JOIN archive_placements p ON p.archive_id=a.id WHERE r.id=? ORDER BY a.id,p.module_id,p.version`, submissionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	count := 0
	for rows.Next() {
		values := make([]string, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return "", err
		}
		if err := encoder.Encode(values); err != nil {
			return "", err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count == 0 {
		return "", ErrNotFound
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// EditSubmission updates a reviewed submission without transferring ownership,
// replacing its stable download IDs or changing its moderation state.
func (s *Service) EditSubmission(ctx context.Context, userID int64, submission SubmissionDraft, expected string) (result UploadResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, storageOperationTimeout)
	defer cancel()
	role, err := accountRole(ctx, s.db, userID)
	if err != nil {
		return result, err
	}
	if !canReview(role) {
		return result, ErrForbidden
	}
	if submission.SubmissionID == "" {
		return result, ErrNotFound
	}
	submission.RevisionOf = ""
	if fields := submission.Validate(); len(fields) > 0 {
		return result, fields
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	publicSubmissionID := submission.SubmissionID
	submission.SubmissionID, err = resolveSubmission(ctx, tx, submission.SubmissionID)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE archive_requests SET id=id WHERE id=?", submission.SubmissionID); err != nil {
		return result, err
	}
	role, err = accountRole(ctx, tx, userID)
	if err != nil {
		return result, err
	}
	if !canReview(role) {
		return result, ErrForbidden
	}
	var status Status
	if err := tx.QueryRowContext(ctx, "SELECT status FROM archive_requests WHERE id=?", submission.SubmissionID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	if status != Pending && status != Approved && status != Rejected {
		return result, ErrConflict
	}
	current, err := submissionEditToken(ctx, tx, submission.SubmissionID)
	if err != nil {
		return result, err
	}
	if expected == "" || current != expected {
		return result, ErrConflict
	}
	metadata, err := prepare(ctx, tx, userID, submission)
	if err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,is_latest FROM archive WHERE request_id=? AND deleted_at IS NULL ORDER BY id", submission.SubmissionID)
	if err != nil {
		return result, err
	}
	var ids []int64
	for rows.Next() {
		var id, latest int64
		if err := rows.Scan(&id, &latest); err != nil {
			rows.Close()
			return result, err
		}
		if status == Approved && latest == 0 {
			rows.Close()
			return result, ErrConflict
		}
		ids = append(ids, id)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return result, err
	}
	if len(ids) != 1 {
		return result, ErrConflict
	}
	// This first transaction only validates the edit. Do not retain its pool
	// slot or writer lock while verifying/uploading the replacement PDF.
	if err := tx.Rollback(); err != nil {
		return result, err
	}
	storage, ok := s.storage.(writableStorage)
	if !ok {
		return result, ErrStorage
	}
	var attempts []string
	committed := false
	defer func() {
		if committed {
			return
		}
		if tx != nil {
			tx.Rollback()
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		for _, key := range attempts {
			if cleanupErr := storage.Delete(cleanup, key); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove uncommitted object: %w", cleanupErr))
				_, queueErr := s.db.ExecContext(cleanup, "INSERT OR IGNORE INTO archive_blob_cleanup(blob_key) VALUES(?)", key)
				err = errors.Join(err, queueErr)
			}
		}
	}()
	var object stagedObject
	if len(submission.Documents) > 0 {
		object, err = stageObject(ctx, s.db, storage, submission.Documents[0], &attempts)
		if err != nil {
			return result, err
		}
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE archive_requests SET id=id WHERE id=?", submission.SubmissionID); err != nil {
		return result, err
	}
	role, err = accountRole(ctx, tx, userID)
	if err != nil {
		return result, err
	}
	if !canReview(role) {
		return result, ErrForbidden
	}
	current, err = submissionEditToken(ctx, tx, submission.SubmissionID)
	if err != nil {
		return result, err
	}
	if current != expected {
		return result, ErrConflict
	}
	metadata, err = prepare(ctx, tx, userID, submission)
	if err != nil {
		return result, err
	}
	var comment *string
	if value := strings.TrimSpace(submission.Comment); value != "" {
		comment = &value
	}
	if len(submission.Documents) > 0 {
		value := submission.Documents[0]
		checksum, err := object.attach(ctx, tx)
		if err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE archive SET checksum=?,nbytes=?,mime_type='application/pdf' WHERE id=?", checksum, len(value), ids[0]); err != nil {
			return result, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE archive SET module_id=?,version=?,exam_date=?,comment=?,updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id=?`, metadata.moduleID, submission.placements()[0].Version, submission.Date, comment, ids[0]); err != nil {
		return result, err
	}
	if err := writeAssignments(ctx, tx, ids[0], submission.placements()); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO archive_blob_cleanup(blob_key) SELECT blob_key FROM archive_objects o WHERE NOT EXISTS(SELECT 1 FROM archive a WHERE a.checksum=o.checksum)`); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO archive_blob_cleanup(blob_key) SELECT c.blob_key FROM archive_legacy_copies c WHERE NOT EXISTS(SELECT 1 FROM archive a WHERE a.checksum=c.checksum)`); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM archive_objects WHERE NOT EXISTS(SELECT 1 FROM archive a WHERE a.checksum=archive_objects.checksum)`); err != nil {
		return result, err
	}
	documentID, err := lookupID(ctx, tx, "documents", ids[0])
	if err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	committed = true
	audit.Audit(ctx, "submission.edited", "success", userID, slog.String("submission_id", publicSubmissionID))
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.CleanupBlobs(cleanup); err != nil {
		log.Printf("Remove replaced exam object: %v", err)
	}
	return UploadResult{SubmissionID: publicSubmissionID, ModuleID: submission.placements()[0].ModuleID, IDs: []string{documentID}}, nil
}
