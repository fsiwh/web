package exams

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/form"
	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/model/meetings"
)

const MaxUploadBytes = 32 << 20

var ErrForbidden = errors.New("Du hast keine Berechtigung, diese Altklausur zu ändern.")

type SubmissionDraft struct {
	ModuleID, Version, Date, Comment, RevisionOf string
	SubmissionID                                 string `json:"RequestID"`
	Documents                                    [][]byte
	Assignments                                  []Assignment
}

type ValidationErrors map[string]string

func (e ValidationErrors) Error() string { return "Prüfe die Angaben im Formular." }

func (s SubmissionDraft) Validate() ValidationErrors {
	result := ValidationErrors{}
	if len(s.Assignments) > 1024 {
		result["module_id"] = "Wähle höchstens 1024 Zuordnungen aus."
	}
	if len(s.Assignments) == 0 && s.ModuleID == "" {
		result["module_id"] = "Wähle ein Modul aus."
	}
	if len(s.Assignments) == 0 && s.Version == "" {
		result["version"] = "Wähle eine Prüfungsordnung aus."
	}
	date, err := time.Parse("2006-01-02", s.Date)
	if err != nil || date.Year() < 1900 || date.Year() > 2100 {
		result["date"] = "Gib ein gültiges Prüfungsdatum zwischen 1900 und 2100 ein."
	}
	if len(s.Documents) == 0 && s.SubmissionID == "" {
		result["files"] = "Wähle eine Datei aus."
	}
	total := 0
	for _, document := range s.Documents {
		if message := meetings.PDFError(document); message != "" {
			result["files"] = message
			break
		}
		total += len(document)
	}
	if total > MaxUploadBytes {
		result["files"] = "Wähle genau eine Datei aus."
	}
	if len(s.Documents) > 1 {
		result["files"] = "Wähle genau eine Datei aus."
	}
	if s.RevisionOf != "" && len(s.Documents) != 1 && !(s.SubmissionID != "" && len(s.Documents) == 0) {
		result["files"] = "Wähle genau eine Datei für die neue Revision aus."
	}
	return result
}

type writableStorage interface {
	Storage
	Upload(context.Context, string, io.Reader, int64, string) error
	Delete(context.Context, string) error
}

type UploadResult struct {
	ModuleID     string
	SubmissionID string `json:"RequestID"`
	IDs          []string
}

type prepared struct {
	moduleID, groupID, editVersion int64
	baseID                         int64
}

// prepare rechecks account policy and revision ownership inside the write
// transaction as well as before contacting object storage.
func prepare(ctx context.Context, db database.DBTX, userID int64, submission SubmissionDraft) (prepared, error) {
	var role string
	err := db.QueryRowContext(ctx, `SELECT role FROM users WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL`, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return prepared{}, ErrForbidden
	}
	if err != nil {
		return prepared{}, err
	}
	selected := submission.placements()
	for _, a := range selected {
		moduleID, err := resolveID(ctx, db, "modules", a.ModuleID)
		if err != nil {
			return prepared{}, ValidationErrors{"module_id": "Wähle ein gültiges Modul aus."}
		}
		var valid int
		err = db.QueryRowContext(ctx, `SELECT 1 FROM modules m JOIN programs p ON p.id=m.program_id AND p.deleted_at IS NULL JOIN program_versions v ON v.program_id=p.id AND v.name=? WHERE m.id=? AND m.deleted_at IS NULL`, a.Version, moduleID).Scan(&valid)
		if errors.Is(err, sql.ErrNoRows) {
			return prepared{}, ValidationErrors{"module_id": "Wähle gültige Module und dazugehörige Prüfungsordnungen aus."}
		}
		if err != nil {
			return prepared{}, err
		}
	}
	moduleID, err := resolveID(ctx, db, "modules", selected[0].ModuleID)
	if err != nil {
		return prepared{}, ValidationErrors{"module_id": "Wähle ein gültiges Modul aus."}
	}
	var programID int64
	err = db.QueryRowContext(ctx, `SELECT m.program_id FROM modules m JOIN programs p ON p.id=m.program_id AND p.deleted_at IS NULL WHERE m.id=? AND m.deleted_at IS NULL`, moduleID).Scan(&programID)
	if errors.Is(err, sql.ErrNoRows) {
		return prepared{}, ValidationErrors{"module_id": "Wähle ein gültiges Modul aus."}
	}
	if err != nil {
		return prepared{}, err
	}
	var exists int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM program_versions WHERE program_id=? AND name=?`, programID, selected[0].Version).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return prepared{}, ValidationErrors{"version": "Wähle eine Prüfungsordnung aus, die zu diesem Modul gehört."}
	}
	if err != nil {
		return prepared{}, err
	}
	result := prepared{moduleID: moduleID, editVersion: 1}
	if submission.RevisionOf == "" {
		return result, nil
	}
	parentID, err := resolveID(ctx, db, "documents", submission.RevisionOf)
	if err != nil {
		return prepared{}, ErrNotFound
	}
	parent, err := canonicalEntry(ctx, db, parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return prepared{}, ErrNotFound
	}
	if err != nil {
		return prepared{}, err
	}
	var status string
	err = db.QueryRowContext(ctx, `SELECT COALESCE(r.status,'approved') FROM archive a LEFT JOIN archive_requests r ON r.id=a.request_id WHERE a.id=?`, parent.ID).Scan(&status)
	if err != nil {
		return prepared{}, err
	}
	if status != "approved" {
		return prepared{}, ErrNotFound
	}
	var ownerID, latestVersion int64
	err = db.QueryRowContext(ctx, `SELECT user_id FROM archive WHERE group_id=? ORDER BY edit_version,id LIMIT 1`, parent.GroupID).Scan(&ownerID)
	if err != nil {
		return prepared{}, err
	}
	if role != "admin" && role != "editor" && userID != ownerID {
		return prepared{}, ErrForbidden
	}
	err = db.QueryRowContext(ctx, `SELECT MAX(edit_version) FROM archive WHERE group_id=?`, parent.GroupID).Scan(&latestVersion)
	if err != nil {
		return prepared{}, err
	}
	result.groupID, result.editVersion = parent.GroupID, latestVersion+1
	latest, err := database.New(db).GetLatestByGroupId(ctx, parent.GroupID)
	if err != nil {
		return prepared{}, err
	}
	result.baseID = latest.ID
	return result, nil
}

// SaveDraft stores uploads without changing publicly available exam documents.
func (s *Service) SaveDraft(ctx context.Context, userID int64, submission SubmissionDraft) (result UploadResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, storageOperationTimeout)
	defer cancel()
	if fields := submission.Validate(); len(fields) != 0 {
		return result, fields
	}
	if _, err := prepare(ctx, s.db, userID, submission); err != nil {
		return result, err
	}
	if submission.SubmissionID != "" {
		current, err := s.Submission(ctx, userID, submission.SubmissionID, false)
		if err != nil {
			return result, err
		}
		if current.Status != Draft {
			return result, ErrConflict
		}
		if current.RevisionOf != submission.RevisionOf {
			return result, ErrConflict
		}
	}
	storage, ok := s.storage.(writableStorage)
	if !ok {
		return result, ErrStorage
	}
	var attempts []string
	var tx *sql.Tx
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
	objects := make([]stagedObject, len(submission.Documents))
	for i, document := range submission.Documents {
		objects[i], err = stageObject(ctx, s.db, storage, document, &attempts)
		if err != nil {
			return result, err
		}
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if submission.SubmissionID != "" {
		submission.SubmissionID, err = resolveSubmission(ctx, tx, submission.SubmissionID)
		if err != nil {
			return result, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE archive_requests SET id=id WHERE id=?", submission.SubmissionID); err != nil {
		return result, err
	}
	metadata, err := prepare(ctx, tx, userID, submission)
	if err != nil {
		return result, err
	}
	submissionID := submission.SubmissionID
	if submissionID == "" {
		submissionID = publicid.New()
		var base any
		if metadata.baseID != 0 {
			base = metadata.baseID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO archive_requests (id,user_id,revision_of) VALUES (?,?,?)`, submissionID, userID, base); err != nil {
			return result, err
		}
	} else {
		var owner int64
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT user_id,status FROM archive_requests WHERE id=?`, submissionID).Scan(&owner, &status); err != nil {
			return result, err
		}
		if owner != userID || status != "draft" {
			return result, ErrConflict
		}
		if len(submission.Documents) != 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE archive SET deleted_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE request_id=? AND deleted_at IS NULL`, submissionID); err != nil {
				return result, err
			}
		}
	}
	var comment *string
	if value := strings.TrimSpace(submission.Comment); value != "" {
		comment = &value
	}
	if len(submission.Documents) == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE archive SET module_id=?,version=?,exam_date=?,comment=? WHERE request_id=? AND deleted_at IS NULL`, metadata.moduleID, submission.placements()[0].Version, submission.Date, comment, submissionID); err != nil {
			return result, err
		}
	}
	seen := map[string]bool{}
	for i, document := range submission.Documents {
		checksum, err := objects[i].attach(ctx, tx)
		if err != nil {
			return result, err
		}
		if seen[checksum] {
			continue
		}
		seen[checksum] = true
		entryID, err := publicid.NextInteger(ctx, tx, "documents")
		if err != nil {
			return result, err
		}
		groupID := metadata.groupID
		if groupID == 0 {
			groupID = entryID
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO archive(id,group_id,user_id,module_id,version,exam_date,comment,edit_version,is_latest,access_key,mime_type,nbytes,checksum,request_id) VALUES(?,?,?,?,?,?,?,?,0,?,'application/pdf',?,?,?)`, entryID, groupID, userID, metadata.moduleID, submission.placements()[0].Version, submission.Date, comment, metadata.editVersion, rand.Text(), len(document), checksum, submissionID)
		if err != nil {
			return result, err
		}
		id, err := publicid.Ensure(ctx, tx, "documents", strconv.FormatInt(entryID, 10))
		if err != nil {
			return result, err
		}
		result.IDs = append(result.IDs, id)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM archive WHERE request_id=? AND deleted_at IS NULL ORDER BY id", submissionID)
	if err != nil {
		return result, err
	}
	var entries []int64
	for rows.Next() {
		var entryID int64
		if err := rows.Scan(&entryID); err != nil {
			rows.Close()
			return result, err
		}
		entries = append(entries, entryID)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return result, err
	}
	for _, entryID := range entries {
		if err := writeAssignments(ctx, tx, entryID, submission.placements()); err != nil {
			return result, err
		}
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
	publicSubmissionID, err := publicid.Ensure(ctx, tx, "submissions", submissionID)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return UploadResult{}, err
	}
	committed = true
	result.ModuleID = submission.placements()[0].ModuleID
	result.SubmissionID = publicSubmissionID
	audit.Audit(ctx, "submission.draft_saved", "success", userID, slog.String("submission_id", publicSubmissionID), slog.Int("documents", len(entries)))
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.CleanupBlobs(cleanup); err != nil {
		log.Printf("Remove replaced draft object: %v", err)
	}
	return result, nil
}

type DateInput form.DateInput

func (d *DateInput) Parse() (string, string) {
	return (*form.DateInput)(d).Parse("Prüfungsdatum")
}
