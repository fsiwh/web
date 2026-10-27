package site

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/fachschaftinformatik/web/internal/datetime"
	"html"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/internal/testdb"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/model/users"
	"golang.org/x/crypto/bcrypt"
)

// publicRef allocates the same registry entry that a production write creates.
// SQL fixtures run after migrations, so they must explicitly create their refs.
func publicRef(t *testing.T, db *sql.DB, namespace, recordID string) string {
	t.Helper()
	ref, err := publicid.Ensure(context.Background(), db, namespace, recordID)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func seedPublicRefs(t *testing.T, db *sql.DB) {
	t.Helper()
	for namespace, table := range map[string]string{
		"users": "users", "members": "users", "roles": "functions",
		"posts": "discussion_posts", "comments": "discussion_comments",
		"events": "events", "albums": "events", "media": "media",
		"programs": "programs", "modules": "modules", "documents": "archive",
		"submissions":    "archive_requests",
		"study_programs": "study_programs",
	} {
		rows, err := db.Query("SELECT id FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			publicRef(t, db, namespace, id)
		}
	}
}

// htmlContains keeps markup assertions independent of template indentation.
func htmlContains(body, snippet string) bool {
	pattern := regexp.QuoteMeta(snippet)
	pattern = strings.ReplaceAll(pattern, " ", `\s+`)
	pattern = strings.ReplaceAll(pattern, ">", `\s*>\s*`)
	return regexp.MustCompile(pattern).MatchString(body)
}

// testApplication always uses the same migrated SQLite runtime as production.
func testApplication(t *testing.T, records ...users.User) (*Handler, *sql.DB) {
	t.Helper()
	path := testdb.Path(t)
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("test password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES (900,'admin@fsv-wh.de','Administrator',?,'admin',1,1),(901,'other@fsv-wh.de','Other administrator',?,'admin',1,1)`, string(hash), string(hash))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		records = []users.User{{ID: 42, Name: "Kim Muster", Email: "kim@studmail.w-hs.de", Active: true}, {ID: 43, Name: "Alex Beispiel", Email: "alex@studmail.w-hs.de", Active: true}}
	}
	for _, user := range records {
		if _, err := db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified) VALUES(?,?,?,?,'user',1,?)`, user.ID, strings.ToLower(strings.TrimSpace(user.Email)), user.Name, string(hash), user.Verified); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := New(persistentServices(t, db, nil), false)
	if err != nil {
		t.Fatal(err)
	}
	return handler, db
}

func testHandler(t *testing.T) (*Handler, error) {
	t.Helper()
	handler, db := testApplication(t)
	seedPublicContent(t, db)
	return handler, nil
}

func seedPublicContent(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var threads []posts.Thread
	var posts []posts.Post
	for path, target := range map[string]any{"snapshot.json": &posts, "threads.json": &threads} {
		data, err := os.ReadFile("../model/posts/testdata/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
	}
	for _, post := range posts {
		exec(`INSERT INTO discussion_posts(id,title,body,user_id,type,created_at) VALUES(?,?,?,900,?,?)`, post.ID, post.Title, html.UnescapeString(post.Body), post.Type, post.Created)
		for _, tag := range post.Tags {
			exec(`INSERT INTO discussion_post_tags(post_id,tag) VALUES(?,?)`, post.ID, tag)
		}
	}
	for _, thread := range threads {
		if len(thread.EventDate) == 16 {
			var err error
			thread.EventDate, err = datetime.LocalTimestamp(thread.EventDate)
			if err != nil {
				t.Fatal(err)
			}
		}
		exec(`UPDATE discussion_posts SET event_date=?,location=? WHERE id=?`, thread.EventDate, thread.Location, thread.ID)
		for _, comment := range thread.Replies {
			exec(`INSERT INTO discussion_comments(id,post_id,user_id,text,created_at,updated_at) VALUES(?,?,900,?,?,?)`, comment.ID, thread.ID, html.UnescapeString(comment.Text), comment.Created, comment.Updated)
		}
	}
	exec(`INSERT INTO programs(id,name) VALUES(1,'Informatik'),(2,'Wirtschaftsinformatik'),(3,'Medieninformatik');
INSERT INTO study_programs(id,name) VALUES(1,'Informatik'),(2,'Wirtschaftsinformatik'),(3,'Medieninformatik');
INSERT INTO program_qualifications(legacy_program_id,study_program_id,degree) VALUES(1,1,'Bachelor'),(2,2,'Bachelor'),(3,3,'Bachelor');
INSERT INTO program_versions(program_id,name) VALUES(1,'2017'),(1,'2023'),(2,'2017'),(2,'2023'),(3,'2023');
INSERT INTO modules(id,program_id,name) VALUES(10,1,'Algorithmen und Datenstrukturen'),(11,2,'Datenbanken'),(12,1,'Mathematik I'),(13,3,'Web Engineering'),(14,1,'Betriebssysteme'),(15,2,'Software Engineering');
UPDATE modules SET alias='SQL' WHERE id=11;`)
	for i := 0; i < 6; i++ {
		version := "2023"
		if i >= 4 {
			version = "2017"
		}
		exec(`INSERT INTO archive(id,group_id,user_id,module_id,version,exam_date,access_key,mime_type,nbytes,checksum) VALUES(?,?,900,?,?, '2026-01-01',?, 'application/pdf',5,'test')`, 100+i, 100+i, 10+i, version, fmt.Sprint(i))
	}
	album := "140125680309370881"
	exec(`INSERT INTO events(id,title,created_at) VALUES(?,'LAN Party 2025','2025-01-01T00:00:00Z')`, album)
	for _, id := range []string{"140126343856652289", "140126331227602945", "140126318313340929", "140126304396640257", "140126291885031425", "140126279717355521"} {
		exec(`INSERT INTO media(id,event_id,user_id,title,description,access_key,mime_type,nbytes,created_at,homepage_visible) VALUES(?,?,900,'Foto','Teilnehmende',?,'image/jpeg',5,'2025-01-01T00:00:00Z',1)`, id, album, id)
	}
	seedPublicRefs(t, db)
}

func (b *adminBrowser) loginAs(email string) {
	b.t.Helper()
	_, body, _ := b.get("/login")
	token := csrfFrom(b.t, body)
	status, _, headers := b.post("/login", url.Values{"email": {email}, "password": {"test password"}, "csrf": {token}})
	if status != 303 || headers.Get("Location") != "/admin" {
		b.t.Fatalf("login failed: %d", status)
	}
	_, body, _ = b.get("/admin")
	b.csrf = csrfFrom(b.t, body)
	if b.csrf == token {
		b.t.Fatal("session not rotated at login")
	}
}
