package users

import (
	"context"
	"testing"
	"time"

	"github.com/fachschaftinformatik/web/internal/datetime"
)

func TestSessionTimeoutsAndRememberedPrivileges(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	for _, role := range []string{"user", "editor", "admin"} {
		t.Run(role, func(t *testing.T) {
			if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(42,'test@council.example.invalid','Test','unused',?,1,1)`, role); err != nil {
				t.Fatal(err)
			}
			defer db.Exec("DELETE FROM users WHERE id=42")
			remembered, err := s.CreateLoginSession(ctx, 42, true)
			if err != nil {
				t.Fatal(err)
			}
			if role == "user" {
				if !remembered.Remembered || !remembered.IdleExpires.IsZero() || time.Until(remembered.Expires) < 13*24*time.Hour {
					t.Fatal("ordinary remembered login not persisted")
				}
				if _, err := db.Exec("UPDATE users SET role='admin' WHERE id=42"); err != nil {
					t.Fatal(err)
				}
				if loaded, err := s.Session(ctx, remembered.ID); err != nil || loaded != nil {
					t.Fatal("remembered login acquired elevated privileges", err)
				}
				if _, err := db.Exec("UPDATE users SET role='user' WHERE id=42"); err != nil {
					t.Fatal(err)
				}
			} else if remembered.Remembered || remembered.IdleExpires.IsZero() || time.Until(remembered.Expires) > 8*time.Hour {
				t.Fatal("privileged login can be remembered")
			}
			session, err := s.CreateSession(ctx, 42)
			if err != nil {
				t.Fatal(err)
			}
			near := datetime.FormatUTC(time.Now().Add(time.Minute))
			if _, err := db.Exec("UPDATE web_sessions SET idle_expires_at=? WHERE id=?", near, digest(session.ID)); err != nil {
				t.Fatal(err)
			}
			loaded, err := s.Session(ctx, session.ID)
			if err != nil || loaded == nil {
				t.Fatal("live session rejected", err)
			}
			var after string
			if err := db.QueryRow("SELECT idle_expires_at FROM web_sessions WHERE id=?", digest(session.ID)).Scan(&after); err != nil || after != near {
				t.Fatal("status lookup extended inactivity", err)
			}
			if err := s.TouchSession(ctx, loaded); err != nil {
				t.Fatal(err)
			}
			if time.Until(loaded.IdleExpires) < 29*time.Minute || !loaded.Expires.Equal(session.Expires) {
				t.Fatal("activity did not renew idle timeout or changed absolute timeout")
			}
			if _, err := db.Exec("UPDATE web_sessions SET expires_at=?,idle_expires_at=? WHERE id=?", near, near, digest(session.ID)); err != nil {
				t.Fatal(err)
			}
			loaded, err = s.Session(ctx, session.ID)
			if err != nil || loaded == nil {
				t.Fatal(err)
			}
			if err := s.TouchSession(ctx, loaded); err != nil || !loaded.Deadline().Equal(loaded.Expires) {
				t.Fatal("absolute timeout was extended", err)
			}
			if _, err := db.Exec("UPDATE web_sessions SET idle_expires_at='2000-01-01T00:00:00Z' WHERE id=?", digest(session.ID)); err != nil {
				t.Fatal(err)
			}
			if err := s.TouchSession(ctx, loaded); err == nil {
				t.Fatal("expired idle session revived")
			}
			if current, err := s.Session(ctx, session.ID); err != nil || current != nil {
				t.Fatal("expired idle session accepted", err)
			}
		})
	}
}
