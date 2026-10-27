package members

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/model/users"
)

func Open(ctx context.Context, db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh(ctx)
}

func (s *Store) refresh(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(NULLIF(public_name,''),name),public_email,email,verified=1,role FROM users WHERE deleted_at IS NULL ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return err
	}
	var records []Member
	for rows.Next() {
		var p Member
		if err := rows.Scan(&p.UserID, &p.Name, &p.Email, &p.AccountEmail, &p.Confirmed, &p.AccessRole); err != nil {
			rows.Close()
			return err
		}
		p.ID = strconv.FormatInt(p.UserID, 10)
		records = append(records, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for i := range records {
		records[i].PublicID, err = publicid.Ensure(ctx, s.db, "users", records[i].ID)
		if err != nil {
			return err
		}
		records[i].UserPublicID = records[i].PublicID
	}
	rows, err = s.db.QueryContext(ctx, `SELECT f.id,f.group_name,f.title,f.email,f.organization_id,o.name FROM functions f JOIN organizations o ON o.id=f.organization_id ORDER BY f.position,f.id`)
	if err != nil {
		return err
	}
	var roles []MemberRole
	for rows.Next() {
		var r MemberRole
		if err := rows.Scan(&r.ID, &r.Group, &r.Title, &r.Email, &r.OrganizationID, &r.Organization); err != nil {
			rows.Close()
			return err
		}
		roles = append(roles, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for i := range roles {
		roles[i].PublicID, err = publicid.Ensure(ctx, s.db, "roles", roles[i].ID)
		if err != nil {
			return err
		}
	}
	rows, err = s.db.QueryContext(ctx, `SELECT function_id,CAST(user_id AS TEXT) FROM user_functions ORDER BY position,user_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var roleID, userID string
		if err := rows.Scan(&roleID, &userID); err != nil {
			rows.Close()
			return err
		}
		for i := range roles {
			if roles[i].ID == roleID {
				roles[i].MemberIDs = append(roles[i].MemberIDs, userID)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	s.members, s.roles = records, roles
	return nil
}

func (s *Store) Resend(ctx context.Context, identity *users.Service, userID string) (bool, error) {
	id, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return false, err
	}
	return identity.ResendConfirmation(ctx, id)
}

func (s *Store) ResolvePublicID(ctx context.Context, value string) (string, error) {
	return publicid.Resolve(ctx, s.db, "users", value)
}

func (s *Store) InviteAccount(ctx context.Context, identity *users.Service, name, email, role string, functions []string) (map[string]string, error) {
	fields := map[string]string{}
	name = strings.TrimSpace(name)
	if message := users.NameError(name); message != "" {
		fields["name"] = message
	}
	if message := identity.EmailError(strings.TrimSpace(email)); message != "" {
		fields["email"] = message
	}
	if role != "editor" && role != "admin" {
		fields["role"] = "Wähle eine Zugangsart aus."
	}
	if role == "editor" && len(functions) == 0 {
		fields["functions"] = "Wähle mindestens eine Funktion aus."
	}
	if len(fields) > 0 {
		return fields, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var ids []string
	for _, value := range functions {
		id, err := publicid.Resolve(ctx, tx, "roles", value)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return map[string]string{"functions": "Wähle vorhandene Funktionen aus."}, nil
			}
			return nil, err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM functions WHERE id=?)`, id).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return map[string]string{"functions": "Wähle vorhandene Funktionen aus."}, nil
		}
		ids = append(ids, id)
	}
	user, err := identity.InviteAccess(ctx, tx, name, email, role)
	if errors.Is(err, users.ErrEmailExists) {
		return map[string]string{"email": err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	for position, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_functions(user_id,function_id,position) VALUES(?,?,?) ON CONFLICT DO NOTHING`, user.ID, id, position); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, s.refresh(ctx)
}

func (s *Store) saveLinked(ctx context.Context, p Member) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET name=?,public_email=? WHERE id=? AND deleted_at IS NULL`, p.Name, p.Email, p.UserID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return sql.ErrNoRows
	}
	return err
}

type Organization struct {
	ID   int64
	Name string
}

func (s *Store) Organizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name FROM organizations ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
