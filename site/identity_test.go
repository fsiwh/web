package site

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/model/events"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/model/media"
	"github.com/fachschaftinformatik/web/model/meetings"
	"github.com/fachschaftinformatik/web/model/members"
	"github.com/fachschaftinformatik/web/model/organization"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/model/users"
	objectstore "github.com/fachschaftinformatik/web/storage"
	"github.com/fachschaftinformatik/web/storage/disk"
	"golang.org/x/crypto/bcrypt"
)

func identityBrowser(t *testing.T) (*adminBrowser, *sql.DB) {
	return identityBrowserWithStorage(t, nil)
}

func identityBrowserWithStorage(t *testing.T, storage exams.Storage) (*adminBrowser, *sql.DB) {
	t.Helper()
	path := testdb.Path(t)
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	handler, err := New(persistentServices(t, db, storage), false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newAdminBrowser(t, server), db
}

func persistentServices(t *testing.T, db *sql.DB, storage exams.Storage) Services {
	t.Helper()

	content, err := json.Marshal(organization.Details{
		Name: "Example Council", FormalName: "Example Representative Body", ShortName: "Example Council", Institution: "Example University",
		City: "Example City", Street: "Example Street 1", PostalCode: "12345", Country: "Deutschland", Office: "A1",
		ContactEmail: "contact@example.org", ChairRole: "1", PublicityRole: "6", LegalUpdated: "2026-01-01",
		HostingName: "Example Host", HostingURL: "https://example.org/hosting", PrivacyURL: "https://example.org/privacy",
		SupervisionName: "Example Supervision", SupervisionURL: "https://example.org/supervision",
		PrivacyAuthorityName: "Example Privacy Authority", PrivacyAuthorityURL: "https://example.org/authority",
		SocialLinks:     []organization.Link{{Title: "Social", URL: "https://social.example.org/"}},
		ElectionLinks:   []organization.Link{{Title: "Election", URL: "https://example.org/election"}},
		RegulationLinks: []organization.Link{{Title: "Satzung der Studierendenschaft", URL: "https://example.org/rules.pdf"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organization(id,data) VALUES(1,?) ON CONFLICT(id) DO NOTHING`, string(content)); err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		year int
		date string
	}{{2030, "2030-04-05"}, {2029, "2029-03-04"}, {2028, ""}} {
		file := fmt.Sprintf("%d-example.pdf", record.year)
		data := historicalTestPDF(file)
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		sources, _ := json.Marshal([]string{file})
		if _, err := db.Exec(`INSERT INTO historical_meeting_documents(file,year,number,date,kind,note,bytes,sha256,sources,blob_key) VALUES(?,?,1,?,'Einladung','',?,?,?,?) ON CONFLICT(file) DO NOTHING`, file, record.year, record.date, len(data), digest, string(sources), "meetings/historical/"+digest); err != nil {
			t.Fatal(err)
		}
		publicRef(t, db, "historical_documents", file)
	}
	if _, err = db.Exec(`INSERT OR IGNORE INTO people(id,name,email,position) VALUES('1','Example Chair','chair@example.org',0); INSERT OR IGNORE INTO responsibilities(id,group_name,title,email,position) VALUES('1','board','Vorsitz','chair@example.org',0),('6','departments','Öffentlichkeit','public@example.org',1); INSERT OR IGNORE INTO responsibility_members(responsibility_id,person_id,position) VALUES('1','1',0)`); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	org, err := organization.NewStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	team, err := members.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := storage.(objectstore.Provider)
	if !ok {
		local, err := disk.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { local.Close() })
		provider = local
		storage = local
	}
	meetingStore, err := meetings.NewStore(db, provider)
	if err != nil {
		t.Fatal(err)
	}
	return Services{Audit: audit.NewStore(db), Organization: org, Users: users.NewService(db, "https://example.org", users.DomainPolicy{Registration: []string{"studmail.w-hs.de"}, Accounts: []string{"studmail.w-hs.de", "fsv-wh.de"}}), Exams: exams.NewService(db, storage), Members: team, Meetings: meetingStore, Posts: posts.NewService(db), Media: media.NewService(db, provider), Events: events.NewService(db, provider)}
}

func historicalTestPDF(file string) []byte { return []byte("%PDF-1.4\n" + file + "\n%%EOF") }

func TestPersistentLoginAuthorizationAndLogout(t *testing.T) {
	b, db := identityBrowser(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("existing password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,email,name,password,role,active,verified) VALUES (42,'PERSON@fsv-wh.de','Person',?,'user',1,1)`, string(hash)); err != nil {
		t.Fatal(err)
	}
	seedPublicRefs(t, db)
	_, body, _ := b.get("/login")
	if strings.Contains(body, "Demozugang") {
		t.Fatal("persistent login advertises demo credentials")
	}
	csrf := csrfFrom(t, body)
	if status, _, _ := b.post("/login", url.Values{"email": {"person@fsv-wh.de"}, "password": {"existing password"}}); status != 403 {
		t.Fatal("login accepted missing CSRF")
	}
	if status, _, _ := b.post("/login", url.Values{"email": {"admin"}, "password": {"admin"}, "csrf": {csrf}}); status != 422 {
		t.Fatal("demo credentials accepted")
	}
	status, _, headers := b.post("/login", url.Values{"email": {"person@fsv-wh.de"}, "password": {"existing password"}, "csrf": {csrf}})
	if status != 303 || headers.Get("Location") != "/" {
		t.Fatalf("login: %d %v", status, headers)
	}
	_, body, _ = b.get("/exams")
	rotated := csrfFrom(t, body)
	if rotated == csrf {
		t.Fatal("login did not rotate session")
	}
	if strings.Contains(body, `href="/admin"`) {
		t.Fatal("normal user sees administration link")
	}
	if status, _, _ := b.get("/admin"); status != 403 {
		t.Fatal("normal user accessed administration")
	}
	if _, err := db.Exec(`UPDATE users SET role='admin' WHERE id=42`); err != nil {
		t.Fatal(err)
	}
	if status, body, _ := b.get("/admin/users"); status != 200 || !strings.Contains(body, "person@fsv-wh.de") {
		t.Fatal("live directory or updated role not used")
	}
	// Build a fresh handler and service, keeping only the database and browser cookie.
	handler, err := New(persistentServices(t, db, nil), false)
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(handler)
	defer restarted.Close()
	b.server = restarted
	if status, _, _ := b.get("/admin"); status != 200 {
		t.Fatal("session lost after handler restart")
	}
	if status, _, _ := b.post("/logout", url.Values{"csrf": {rotated}}); status != 303 {
		t.Fatal("logout failed")
	}
	if status, _, headers := b.get("/admin"); status != 303 || headers.Get("Location") != "/login" {
		t.Fatal("logout retained administration access")
	}
}

func mailTokenPath(t *testing.T, db *sql.DB, kind string) string {
	t.Helper()
	var link string
	if err := db.QueryRow(`SELECT link FROM mail_outbox WHERE kind=?`, kind).Scan(&link); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

func TestPersistentRegistrationAndRecoveryJourney(t *testing.T) {
	b, db := identityBrowser(t)
	_, body, _ := b.get("/register")
	csrf := csrfFrom(t, body)
	form := url.Values{"name": {"Kim Beispiel"}, "email": {"KIM@studmail.w-hs.de"}, "password": {"eight 🔐🔐🔐"}, "csrf": {csrf}}
	if status, _, _ := b.post("/register", url.Values{}); status != 403 {
		t.Fatal("registration accepted missing CSRF")
	}
	if status, body, h := b.post("/register", form); status != 303 || h.Get("Location") != "/register" {
		t.Fatalf("registration: %d %s", status, body)
	}
	if status, body, _ := b.get("/register"); status != 200 || !strings.Contains(body, "Dein Konto wurde erstellt.") {
		t.Fatal("registration receipt missing")
	}
	verifyPath := mailTokenPath(t, db, "verify")
	_, body, _ = b.get(verifyPath)
	var verified int
	if err := db.QueryRow(`SELECT verified FROM users WHERE email='kim@studmail.w-hs.de'`).Scan(&verified); err != nil || verified != 0 {
		t.Fatal("GET consumed verification token")
	}
	u, _ := url.Parse(verifyPath)
	if status, _, h := b.post("/email/confirm", url.Values{"csrf": {csrfFrom(t, body)}, "token": {u.Query().Get("token")}}); status != 303 || h.Get("Location") != "/email/confirm" {
		t.Fatal("verification failed")
	}
	_, body, _ = b.get("/password/forgot")
	if status, _, h := b.post("/password/forgot", url.Values{"csrf": {csrfFrom(t, body)}, "email": {"kim@studmail.w-hs.de"}}); status != 303 || h.Get("Location") != "/password/forgot" {
		t.Fatal("reset request failed")
	}
	if status, body, _ := b.get("/password/forgot"); status != 200 || !strings.Contains(body, "Wenn ein Konto") {
		t.Fatal("reset receipt missing")
	}
	resetPath := mailTokenPath(t, db, "reset")
	_, body, _ = b.get(resetPath)
	u, _ = url.Parse(resetPath)
	reset := url.Values{"csrf": {csrfFrom(t, body)}, "token": {u.Query().Get("token")}, "password": {"replacement 🔐"}}
	if status, _, _ := b.post("/password/reset", reset); status != 303 {
		t.Fatal("reset failed")
	}
	if status, _, _ := b.post("/password/reset", reset); status != 422 {
		t.Fatal("reset token reused")
	}
	s := users.NewService(db, "https://example.org", users.DomainPolicy{Registration: []string{"studmail.w-hs.de"}, Accounts: []string{"studmail.w-hs.de", "fsv-wh.de"}})
	if _, err := s.Login(context.Background(), "kim@studmail.w-hs.de", "replacement 🔐"); err != nil {
		t.Fatal(err)
	}
}
