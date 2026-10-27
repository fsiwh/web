package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/mail"
	"strings"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/pressly/goose/v3"
)

func unifyUsers(ctx context.Context, tx *sql.Tx) error {
	var collisions int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT lower(trim(email)) FROM users GROUP BY lower(trim(email)) HAVING count(*)>1)`).Scan(&collisions); err != nil {
		return err
	}
	if collisions != 0 {
		return fmt.Errorf("unify users: resolve %d case-insensitive account email collisions before migrating", collisions)
	}
	// Copy profiles before dropping their tables. Account identity wins for linked users.
	rows, err := tx.QueryContext(ctx, `SELECT id,name,email,user_id,public_name FROM people ORDER BY position,id`)
	if err != nil {
		return err
	}
	type profile struct {
		id, name, email string
		publicName      string
		user            sql.NullInt64
	}
	var profiles []profile
	for rows.Next() {
		var p profile
		if err := rows.Scan(&p.id, &p.name, &p.email, &p.user, &p.publicName); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `
		DROP TRIGGER trg_legal_person_revision;
		DROP TRIGGER trg_legal_account_revision;
		DROP TRIGGER trg_legal_assignment_added;
		DROP TRIGGER trg_legal_assignment_removed;
		UPDATE users SET email=lower(trim(email)) WHERE email<>lower(trim(email));
		CREATE UNIQUE INDEX users_email_nocase ON users(email COLLATE NOCASE);
		CREATE TRIGGER users_email_insert BEFORE INSERT ON users
		WHEN NEW.email<>lower(trim(NEW.email)) BEGIN SELECT RAISE(ABORT,'email must be lowercase and trimmed'); END;
		CREATE TRIGGER users_email_update BEFORE UPDATE OF email ON users
		WHEN NEW.email<>lower(trim(NEW.email)) BEGIN SELECT RAISE(ABORT,'email must be lowercase and trimmed'); END;
		CREATE TABLE organizations (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE) STRICT;
		INSERT INTO organizations(id,name) VALUES(1,'Fachschaft Informatik');
		ALTER TABLE responsibilities RENAME TO functions;
		ALTER TABLE functions ADD COLUMN organization_id INTEGER NOT NULL DEFAULT 1 REFERENCES organizations(id) ON DELETE RESTRICT;
		CREATE UNIQUE INDEX functions_name ON functions(organization_id,group_name,title);
		CREATE TABLE user_functions (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			function_id TEXT NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
			position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(user_id,function_id)
		) STRICT;
		CREATE INDEX user_functions_function ON user_functions(function_id,position);
	`); err != nil {
		return err
	}
	seen := map[int64]string{}
	for _, p := range profiles {
		if !p.user.Valid {
			email := strings.ToLower(strings.TrimSpace(p.email))
			address, err := mail.ParseAddress(email)
			if err != nil || address.Address != email {
				return fmt.Errorf("unify users: profile %q (%s) needs a usable account email before migrating", p.id, p.name)
			}
			err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email=? COLLATE NOCASE AND deleted_at IS NULL`, email).Scan(&p.user.Int64)
			if err == sql.ErrNoRows {
				id, err := publicid.NextInteger(ctx, tx, "users")
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,email,name,password,role,active,invitation_pending) VALUES(?,?,?,'!','editor',1,1)`, id, email, p.name); err != nil {
					return err
				}
				p.user.Int64 = id
			} else if err != nil {
				return err
			}
		}
		if previous, ok := seen[p.user.Int64]; ok {
			return fmt.Errorf("unify users: profiles %q and %q resolve to the same account; resolve the duplicate before migrating", previous, p.id)
		}
		seen[p.user.Int64] = p.id
		if _, err := tx.ExecContext(ctx, `UPDATE users SET public_name=? WHERE id=? AND public_name<>?`, p.publicName, p.user.Int64, p.publicName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET public_email=? WHERE id=? AND public_email<>?`, p.email, p.user.Int64, p.email); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET invitation_pending=1 WHERE id=? AND verified=0 AND invitation_pending=0`, p.user.Int64); err != nil {
			return err
		}
		if _, err := publicid.Ensure(ctx, tx, "users", fmt.Sprint(p.user.Int64)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_functions(user_id,function_id,position) SELECT ?,responsibility_id,position FROM responsibility_members WHERE person_id=? ON CONFLICT(user_id,function_id) DO NOTHING`, p.user.Int64, p.id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE public_ids SET retired=1 WHERE namespace='members';
		DROP TABLE responsibility_members;
		DROP TABLE people;
		DROP TABLE people_state;
		CREATE TRIGGER trg_legal_account_revision AFTER UPDATE OF name,public_name ON users
		WHEN (OLD.name IS NOT NEW.name OR OLD.public_name IS NOT NEW.public_name) AND EXISTS (
			SELECT 1 FROM user_functions uf JOIN organization o ON uf.function_id=json_extract(o.data,'$.ChairResponsibility') WHERE uf.user_id=NEW.id
		) BEGIN UPDATE organization SET data=json_set(data,'$.LegalUpdated',strftime('%Y-%m-%d','now')) WHERE id=1; END;
		CREATE TRIGGER trg_legal_assignment_added AFTER INSERT ON user_functions
		WHEN NEW.function_id=(SELECT json_extract(data,'$.ChairResponsibility') FROM organization WHERE id=1)
		BEGIN UPDATE organization SET data=json_set(data,'$.LegalUpdated',strftime('%Y-%m-%d','now')) WHERE id=1; END;
		CREATE TRIGGER trg_legal_assignment_removed AFTER DELETE ON user_functions
		WHEN OLD.function_id=(SELECT json_extract(data,'$.ChairResponsibility') FROM organization WHERE id=1)
		BEGIN UPDATE organization SET data=json_set(data,'$.LegalUpdated',strftime('%Y-%m-%d','now')) WHERE id=1; END;
	`)
	return err
}

func unifiedUsersMigration() *goose.Migration {
	return goose.NewGoMigration(31, &goose.GoFunc{RunTx: unifyUsers}, nil)
}
