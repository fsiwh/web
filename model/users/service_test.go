package users

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	"golang.org/x/crypto/bcrypt"
)

func testService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sqlite.db")
	if err := database.Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db, "https://example.org", DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}}), db
}

func TestExistingCredentialsAndPersistentSessionPolicy(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("existing password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,email,name,password,role,active,verified) VALUES (42,'existing@council.example.invalid','Existing',?,'admin',1,1)`, string(hash)); err != nil {
		t.Fatal(err)
	}
	id, err := s.Login(ctx, "existing@council.example.invalid", "existing password")
	if err != nil || id != 42 {
		t.Fatalf("legacy login: %d %v", id, err)
	}
	session, err := s.CreateSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewService(db, "https://example.org", DomainPolicy{Registration: []string{"students.example.invalid"}, Accounts: []string{"students.example.invalid", "council.example.invalid"}})
	loaded, err := restarted.Session(ctx, session.ID)
	if err != nil || loaded == nil || !loaded.CanAdmin() || loaded.CSRF != session.CSRF {
		t.Fatalf("persisted session: %+v %v", loaded, err)
	}
	if _, err := db.Exec(`UPDATE users SET role='user' WHERE id=42`); err != nil {
		t.Fatal(err)
	}
	loaded, err = restarted.Session(ctx, session.ID)
	if err != nil || loaded == nil || loaded.CanAdmin() {
		t.Fatal("role changes did not take effect")
	}
	if _, err := db.Exec(`UPDATE users SET active=0 WHERE id=42`); err != nil {
		t.Fatal(err)
	}
	loaded, err = restarted.Session(ctx, session.ID)
	if err != nil || loaded != nil {
		t.Fatal("inactive user retained session")
	}
	if _, err := s.Login(ctx, "existing@council.example.invalid", "existing password"); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
}

func queuedToken(t *testing.T, db *sql.DB, kind string) string {
	t.Helper()
	var link string
	if err := db.QueryRow(`SELECT link FROM mail_outbox WHERE kind=?`, kind).Scan(&link); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := "/email/confirm"
	if kind == "reset" {
		wantPath = "/password/reset"
	}
	if u.Path != wantPath {
		t.Fatalf("queued %s link path = %q, want %q", kind, u.Path, wantPath)
	}
	return u.Query().Get("token")
}

func TestRegistrationVerificationAndSingleUseReset(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	password := strings.Repeat("🔐", 40)
	if err := s.Register(ctx, "Kim Beispiel", "KIM@students.example.invalid", password, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "kim@students.example.invalid", password); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
	verify := queuedToken(t, db, "verify")
	if err := s.ConsumeToken(ctx, verify, "verify", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeToken(ctx, verify, "verify", ""); !errors.Is(err, ErrToken) {
		t.Fatal("verification link reused")
	}
	userID, err := s.Login(ctx, "kim@students.example.invalid", password)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if session.CanAdmin() {
		t.Fatal("registration granted administration")
	}
	if err := s.RequestReset(ctx, "missing@council.example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestReset(ctx, "kim@students.example.invalid"); err != nil {
		t.Fatal(err)
	}
	reset := queuedToken(t, db, "reset")
	if err := s.ConsumeToken(ctx, reset, "reset", "new password"); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeToken(ctx, reset, "reset", "other password"); !errors.Is(err, ErrToken) {
		t.Fatal("reset link reused")
	}
	if loaded, err := s.Session(ctx, session.ID); err != nil || loaded != nil {
		t.Fatal("password reset retained session")
	}
	if _, err := s.Login(ctx, "kim@students.example.invalid", password); !errors.Is(err, ErrCredentials) {
		t.Fatal("old password still works")
	}
	if _, err := s.Login(ctx, "kim@students.example.invalid", "new password"); err != nil {
		t.Fatal(err)
	}
}

type queuedMailer struct {
	failTo    string
	delivered []string
}

func (m *queuedMailer) SendAccountChangedEmail(to, name, kind string) error {
	return m.SendVerificationEmail(to, name, kind)
}

func (m *queuedMailer) SendVerificationEmail(to, name, link string) error {
	if to == m.failTo {
		return errors.New("SMTP unavailable")
	}
	m.delivered = append(m.delivered, to)
	return nil
}

func (m *queuedMailer) SendPasswordResetEmail(to, name, link string) error {
	return m.SendVerificationEmail(to, name, link)
}

func TestOutboxRetriesAndExpiredTokens(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	for _, email := range []string{"first@students.example.invalid", "second@students.example.invalid"} {
		if err := s.Register(ctx, "Test Person", email, "valid password", "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	mailer := &queuedMailer{failTo: "first@students.example.invalid"}
	if err := s.DeliverMail(ctx, mailer); err == nil {
		t.Fatal("delivery failure not reported")
	}
	if len(mailer.delivered) != 1 || mailer.delivered[0] != "second@students.example.invalid" {
		t.Fatal("one failed message blocked later mail")
	}
	var pending int
	if err := db.QueryRow(`SELECT count(*) FROM mail_outbox`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("failed message not retained")
	}
	mailer.failTo = ""
	if err := s.DeliverMail(ctx, mailer); err != nil {
		t.Fatal(err)
	}
	if len(mailer.delivered) != 2 {
		t.Fatal("retry lost or duplicated mail")
	}
	if err := s.RequestReset(ctx, "first@students.example.invalid"); err != nil {
		t.Fatal(err)
	}
	value := queuedToken(t, db, "reset")
	if _, err := db.Exec(`UPDATE user_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE kind='reset'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeToken(ctx, value, "reset", "changed password"); !errors.Is(err, ErrToken) {
		t.Fatal("expired link accepted")
	}
	if err := s.DeliverMail(ctx, mailer); err != nil {
		t.Fatal(err)
	}
	if len(mailer.delivered) != 2 {
		t.Fatal("expired link was sent")
	}
}

func TestInvalidResetLinkRejectedBeforePasswordWork(t *testing.T) {
	s, _ := testService(t)
	// An invalid link takes precedence even over invalid password input.
	if err := s.ConsumeToken(context.Background(), strings.Repeat("a", 64), "reset", ""); !errors.Is(err, ErrToken) {
		t.Fatalf("unknown link reached password handling: %v", err)
	}
}

func TestInvitationPromotionRevokesExistingSessions(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users (id,email,name,password,role,active,verified) VALUES (42,'existing@council.example.invalid','Existing','!','user',1,1)`); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	user, err := s.Invite(ctx, tx, "Existing", "existing@council.example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if user.Role != "editor" {
		t.Fatal("invitation did not promote member")
	}
	if loaded, err := s.Session(ctx, session.ID); err != nil || loaded != nil {
		t.Fatalf("old session gained editor privileges: %+v %v", loaded, err)
	}
}
