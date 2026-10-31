package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/datetime"
)

type Preferences struct {
	Name, Email, PendingEmail string
}

var ErrCurrentPassword = errors.New("Dein aktuelles Passwort ist falsch.")
var ErrEmailUnchanged = errors.New("Gib eine andere E-Mail-Adresse als deine aktuelle ein.")

func (s *Service) Preferences(ctx context.Context, userID int64) (Preferences, error) {
	var p Preferences
	err := s.db.QueryRowContext(ctx, `SELECT u.name,lower(u.email),COALESCE(c.email,'')
		FROM users u LEFT JOIN account_email_changes c ON c.user_id=u.id
		AND lower(c.previous_email)=lower(u.email)
		AND EXISTS(SELECT 1 FROM user_tokens t WHERE t.id=c.token_id AND t.expires_at>?)
		WHERE u.id=? AND u.active=1 AND u.verified=1 AND u.deleted_at IS NULL`, datetime.FormatUTC(time.Now()), userID).Scan(&p.Name, &p.Email, &p.PendingEmail)
	return p, err
}

func (s *Service) ChangeName(ctx context.Context, userID int64, name string) error {
	name = strings.TrimSpace(name)
	if message := NameError(name); message != "" {
		return errors.New(message)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET name=?,updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL`, name, userID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return sql.ErrNoRows
	}
	audit.Audit(ctx, "account.name_changed", "success", userID)
	return nil
}

// Recheck the credential inside the write transaction as well, so a concurrent
// password reset cannot authorize a change using an obsolete password.
func (s *Service) preferenceCredential(ctx context.Context, userID int64, password string) (string, error) {
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT password FROM users WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL`, userID).Scan(&hash); err != nil {
		return "", err
	}
	if !VerifyPassword(hash, password) {
		audit.Audit(ctx, "account.preferences", "incorrect_password", userID)
		return "", ErrCurrentPassword
	}
	return hash, nil
}

// RequestEmailChange leaves the login address untouched until the new mailbox
// confirms the link. Requesting again replaces the previous token and outbox mail.
func (s *Service) RequestEmailChange(ctx context.Context, userID int64, email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if message := s.EmailError(email); message != "" {
		return errors.New(message)
	}
	hash, err := s.preferenceCredential(ctx, userID, password)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous, name string
	if err := tx.QueryRowContext(ctx, `SELECT email,name FROM users WHERE id=? AND password=? AND active=1 AND verified=1 AND deleted_at IS NULL`, userID, hash).Scan(&previous, &name); errors.Is(err, sql.ErrNoRows) {
		return ErrCurrentPassword
	} else if err != nil {
		return err
	}
	if strings.EqualFold(previous, email) {
		return ErrEmailUnchanged
	}
	var taken bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=?)`, email).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrEmailExists
	}
	// Delete before queueToken updates the token primary key referenced by the
	// pending change. Both operations are rolled back if queuing fails.
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_email_changes WHERE user_id=?`, userID); err != nil {
		return err
	}
	if err := s.queueToken(ctx, tx, userID, email, name, "verify"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_email_changes(user_id,token_id,email,previous_email) SELECT user_id,id,?,? FROM user_tokens WHERE user_id=? AND kind='verify'`, email, previous, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	audit.Audit(ctx, "account.email_change_requested", "mail_queued", userID)
	return nil
}

func (s *Service) ChangePassword(ctx context.Context, userID int64, sessionID, current, password string) error {
	if message := PasswordError(password); message != "" {
		return errors.New(message)
	}
	previous, err := s.preferenceCredential(ctx, userID, current)
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE users SET password=?,password_reset_token=NULL,password_reset_expires=NULL,updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id=? AND password=? AND active=1 AND verified=1 AND deleted_at IS NULL`, hash, userID, previous)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrCurrentPassword
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM web_sessions WHERE user_id=? AND id<>?`, userID, digest(sessionID)); err != nil {
		return err
	}
	if err := database.New(tx).DeleteUserSessions(ctx, userID); err != nil {
		return err
	}
	if err := revokeAccountLinks(ctx, tx, userID); err != nil {
		return err
	}
	if err := queueAccountChange(ctx, tx, userID, "password_changed", ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	audit.Audit(ctx, "account.password_changed", "success", userID)
	audit.Audit(ctx, "session.revoked", "password_changed_other_sessions", userID)
	return nil
}

func revokeAccountLinks(ctx context.Context, tx *sql.Tx, userID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_tokens WHERE user_id=?`, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM mail_outbox WHERE user_id=?`, userID)
	return err
}

func queueAccountChange(ctx context.Context, tx *sql.Tx, userID int64, kind, previousEmail string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO account_change_mail(user_id,kind,recipient,name)
		SELECT id,?,CASE WHEN ?<>'' THEN ? ELSE email END,name FROM users WHERE id=?`, kind, previousEmail, previousEmail, userID)
	return err
}
