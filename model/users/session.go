package users

import (
	"context"
	"database/sql"
	"time"

	"github.com/fachschaftinformatik/web/internal/datetime"
)

const SessionIdleTimeout = 30 * time.Minute

func (s Session) Deadline() time.Time {
	if !s.IdleExpires.IsZero() && s.IdleExpires.Before(s.Expires) {
		return s.IdleExpires
	}
	return s.Expires
}

// TouchSession never revives an expired session or moves its absolute deadline.
func (s *Service) TouchSession(ctx context.Context, session *Session) error {
	if !session.Authenticated || session.IdleExpires.IsZero() {
		return nil
	}
	now := time.Now().UTC()
	idle := minTime(now.Add(SessionIdleTimeout), session.Expires)
	result, err := s.db.ExecContext(ctx, `UPDATE web_sessions SET idle_expires_at=? WHERE id=? AND expires_at>? AND idle_expires_at>?`, datetime.FormatUTC(idle), digest(session.ID), datetime.FormatUTC(now), datetime.FormatUTC(now))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	session.IdleExpires = idle
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
