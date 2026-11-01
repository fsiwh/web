package users

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
)

func TestCreateUserDoesNotReuseDeletedHighestIdentity(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	create := func(email string) database.User {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		user, err := CreateUserTx(ctx, tx, database.CreateUserParams{Email: email, Name: "Test User", Password: "!", Role: "user", Active: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return user
	}
	first := create("first@students.example.invalid")
	previous, err := s.PublicID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	second := create("second@students.example.invalid")
	if second.ID <= first.ID {
		t.Fatalf("internal ID reused: first=%d second=%d", first.ID, second.ID)
	}
	current, err := s.PublicID(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == previous {
		t.Fatal("new account reused a retired public ID")
	}
	if _, err := s.ResolvePublicID(ctx, previous); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retired ID still resolves: %v", err)
	}
}
