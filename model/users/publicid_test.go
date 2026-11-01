package users

import (
	"context"
	"errors"
	"testing"

	"github.com/fachschaftinformatik/web/internal/publicid"
)

func TestPublicProfileUsesPermanentOpaqueIDAndHonorsPrivacy(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,private) VALUES(42,'profile@council.example.invalid','Profile','!',0)`); err != nil {
		t.Fatal(err)
	}
	value, err := s.PublicID(ctx, 42)
	if err != nil || !publicid.Valid(value) {
		t.Fatalf("public ID = %q, %v", value, err)
	}
	profile, err := s.PublicProfile(ctx, value)
	if err != nil || profile.ID != 42 || profile.PublicID != value {
		t.Fatalf("profile = %+v, %v", profile, err)
	}
	if _, err := s.PublicProfile(ctx, "42"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("numeric alias accepted: %v", err)
	}
	directory, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := directory.Get("42"); ok {
		t.Fatal("numeric admin alias accepted")
	}
	if user, ok := directory.Get(value); !ok || user.PublicID != value {
		t.Fatal("public admin ID did not resolve")
	}
	if _, err := db.Exec(`UPDATE users SET private=1 WHERE id=42`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublicProfile(ctx, value); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("private profile exposed: %v", err)
	}
	if again, err := s.PublicID(ctx, 42); err != nil || again != value {
		t.Fatalf("public ID changed: %q, %v", again, err)
	}
}
