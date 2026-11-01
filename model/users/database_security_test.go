package users

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/database"
)

func TestConcurrentVerificationConsumersAreSingleUse(t *testing.T) {
	s, db := testService(t)
	preferenceUser(t, db)
	if _, err := db.Exec("UPDATE users SET verified=0 WHERE id=42"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.queueToken(ctx, tx, 42, "old@studmail.w-hs.de", "Example", "verify"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	value := queuedToken(t, db, "verify")
	var path string
	if err := db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	otherDB, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer otherDB.Close()
	other := NewService(otherDB, "https://example.org", s.Domains())
	start := make(chan struct{})
	finished := make(chan error, 2)
	for _, service := range []*Service{s, other} {
		go func() { <-start; finished <- service.ConsumeToken(ctx, value, "verify", "") }()
	}
	close(start)
	successes, rejected := 0, 0
	for range 2 {
		select {
		case err := <-finished:
			if err == nil {
				successes++
			} else if errors.Is(err, ErrToken) {
				rejected++
			} else {
				t.Fatalf("concurrent consumption returned a database failure: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("token consumers did not finish")
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("consumption was not single-use: successes=%d rejected=%d", successes, rejected)
	}
	var verified, tokens int
	if err := db.QueryRow("SELECT verified FROM users WHERE id=42").Scan(&verified); err != nil || verified != 1 {
		t.Fatal("account not verified", verified, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM user_tokens").Scan(&tokens); err != nil || tokens != 0 {
		t.Fatal("consumed token retained", tokens, err)
	}
}

func TestAccountSQLInputsCannotSelectOrModifyAnotherUser(t *testing.T) {
	s, db := testService(t)
	preferenceUser(t, db)
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(43,'admin@fsv-wh.de','Administrator','hash','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, payload := range []string{"' OR 1=1 --", "x'; DROP TABLE users; --", "x\x00' OR '1'='1", "＇ OR 1=1 --"} {
		if _, err := database.New(db).GetUserByEmail(ctx, payload); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("SQL-looking lookup was not treated as data: %q: %v", payload, err)
		}
		// This traverses the handwritten account update path as well as the
		// generated lookup path. The name must round-trip as data, never SQL.
		err := s.ChangeName(ctx, 42, payload)
		if NameError(payload) != "" {
			if err == nil {
				t.Fatal("invalid name accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal("bound value failed to round-trip", err)
		}
		var name, role string
		if err := db.QueryRow("SELECT name,role FROM users WHERE id=42").Scan(&name, &role); err != nil || name != payload || role != "user" {
			t.Fatal("input changed SQL semantics", name, role, err)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil || count != 2 {
		t.Fatal("input damaged account table", count, err)
	}
	var name, role string
	if err := db.QueryRow("SELECT name,role FROM users WHERE id=43").Scan(&name, &role); err != nil || name != "Administrator" || role != "admin" {
		t.Fatal("input modified another account", name, role, err)
	}
}
