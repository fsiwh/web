package members

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/fachschaftinformatik/web/model/users"
)

var ErrForbidden = errors.New("public profiles require an active verified editor")

// SavePublicName never changes login identity, account linkage or privileges.
func (s *Store) SavePublicName(ctx context.Context, actor int64, id, name string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var allowed int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL AND role IN ('editor','admin')`, actor).Scan(&allowed); err != nil {
		return nil, err
	}
	if allowed != 1 {
		return nil, ErrForbidden
	}
	name = strings.TrimSpace(name)
	if message := users.NameError(name); message != "" {
		return map[string]string{"name": message}, nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET public_name=? WHERE id=? AND deleted_at IS NULL AND (role IN ('editor','admin') OR EXISTS(SELECT 1 FROM user_functions WHERE user_id=users.id))`, name, id)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, sql.ErrNoRows
	}
	return nil, s.refresh(ctx)
}
