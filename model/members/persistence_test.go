package members

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/model/users"
)

func membershipStore(t *testing.T) (*Store, *users.Service, *sql.DB) {
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
	if _, err := db.Exec(`INSERT INTO functions(id,group_name,title,email,position) VALUES('finance','board','Finanzen','finance@example.org',0),('sport','departments','Sport','',1)`); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return s, users.NewService(db, "https://example.org", users.DomainPolicy{Accounts: []string{"fsv-wh.de", "studmail.w-hs.de"}}), db
}

func inviteToken(t *testing.T, db *sql.DB) string {
	t.Helper()
	var link string
	if err := db.QueryRow(`SELECT link FROM mail_outbox WHERE kind='verify'`).Scan(&link); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil || u.Path != "/invitation/accept" {
		t.Fatal("incorrect invitation link", link, err)
	}
	return u.Query().Get("token")
}

func TestAccountInvitationWithMultipleFunctions(t *testing.T) {
	s, identity, db := membershipStore(t)
	ctx := context.Background()
	ids := []string{s.Roles("board")[0].PublicID, s.Roles("departments")[0].PublicID}
	if fields, err := s.InviteAccount(ctx, identity, "Alex Example", "ALEX@FSV-WH.DE", "editor", ids); err != nil || len(fields) > 0 {
		t.Fatal(fields, err)
	}
	u := s.Members()[0]
	if u.UserID == 0 || u.Confirmed || u.AccountEmail != "alex@fsv-wh.de" || u.Email != "" {
		t.Fatal(u)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM user_functions WHERE user_id=?`, u.UserID).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	previous := inviteToken(t, db)
	if queued, err := s.Resend(ctx, identity, u.ID); err != nil || !queued {
		t.Fatal(queued, err)
	}
	current := inviteToken(t, db)
	if current == previous {
		t.Fatal("resend did not rotate token")
	}
	if _, err := identity.Invitation(ctx, previous); !errors.Is(err, users.ErrToken) {
		t.Fatal("old token still usable", err)
	}
	if err := identity.ConsumeToken(ctx, current, "verify", ""); !errors.Is(err, users.ErrToken) {
		t.Fatal("invitation bypassed password setup", err)
	}
	if err := identity.AcceptInvitation(ctx, current, "existing password"); err != nil {
		t.Fatal(err)
	}
	if err := identity.AcceptInvitation(ctx, current, "another password"); !errors.Is(err, users.ErrToken) {
		t.Fatal("token reused", err)
	}
	if id, err := identity.Login(ctx, "alex@fsv-wh.de", "existing password"); err != nil || id != u.UserID {
		t.Fatal(id, err)
	}
	restarted, err := Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.Members()) != 1 || len(restarted.Roles("board")[0].MemberIDs) != 1 {
		t.Fatal("restart lost identity or assignments")
	}
	for _, g := range restarted.Groups() {
		for _, section := range g.Sections {
			for _, p := range section.Members {
				if p.Email == u.AccountEmail {
					t.Fatal("login email published")
				}
			}
		}
	}
}

func TestAdminInvitationAndExistingCredentials(t *testing.T) {
	s, identity, db := membershipStore(t)
	ctx := context.Background()
	if fields, err := s.InviteAccount(ctx, identity, "Robin Example", "robin@fsv-wh.de", "admin", nil); err != nil || len(fields) > 0 {
		t.Fatal(fields, err)
	}
	if err := identity.AcceptInvitation(ctx, inviteToken(t, db), "existing password"); err != nil {
		t.Fatal(err)
	}
	u := s.Members()[0]
	if session, err := identity.CreateSession(ctx, u.UserID); err != nil || !session.CanAdmin() {
		t.Fatal(session, err)
	}
	var hash string
	if err := db.QueryRow(`SELECT password FROM users WHERE id=?`, u.UserID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if fields, err := s.InviteAccount(ctx, identity, "Ignored Name", "robin@fsv-wh.de", "editor", []string{s.Roles("board")[0].PublicID}); err != nil || len(fields) > 0 {
		t.Fatal(fields, err)
	}
	var role, password, name string
	if err := db.QueryRow(`SELECT role,password,name FROM users WHERE id=?`, u.UserID).Scan(&role, &password, &name); err != nil || role != "admin" || password != hash || name != "Robin Example" {
		t.Fatal("existing identity changed", role, name, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM mail_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("confirmed account got setup mail", err)
	}
}

func TestInvitationValidationAndAtomicity(t *testing.T) {
	s, identity, db := membershipStore(t)
	ctx := context.Background()
	for _, ids := range [][]string{nil, {"AAAAAAAAAAA"}} {
		if fields, err := s.InviteAccount(ctx, identity, "Alex Example", "alex@fsv-wh.de", "editor", ids); err != nil || fields["functions"] == "" {
			t.Fatal(fields, err)
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_assignment BEFORE INSERT ON user_functions BEGIN SELECT RAISE(FAIL,'forced assignment failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InviteAccount(ctx, identity, "Alex Example", "alex@fsv-wh.de", "editor", []string{s.Roles("board")[0].PublicID}); err == nil {
		t.Fatal("assignment failure ignored")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed invitation left account", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM mail_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed invitation left mail", err)
	}
}
