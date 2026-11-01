package posts

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
)

func TestSQLPostFiltersPrecedePagingAndCommentsAreBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "posts.db")
	if err := database.Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,name,password,private) VALUES(1,'public@council.example.invalid','Public','unused',0)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 45; i++ {
		kind := "discussion"
		if i > 30 {
			kind = "news"
		}
		if _, err := db.Exec(`INSERT INTO discussion_posts(id,title,body,user_id,type,created_at,pinned) VALUES(?,?,'token',1,?,'2026-01-01T00:00:00Z',?)`, i, fmt.Sprintf("Title %d", i), kind, i == 1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO discussion_post_tags(post_id,tag) VALUES(?,'match')`, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE discussion_posts SET deleted_at='2026-01-01T00:00:00Z' WHERE id=5`); err != nil {
		t.Fatal(err)
	}
	s := NewService(db)
	seedReferences(t, db)
	ctx := context.Background()
	seen := map[string]bool{}
	for page := 1; page <= 5; page++ {
		records, total, err := s.FindPage(ctx, Filter{Type: "discussion", Query: "token match"}, page, 7)
		want := 7
		if page == 5 {
			want = 1
		}
		if err != nil || total != 29 || len(records) != want {
			t.Fatalf("page %d: %d records total %d %v", page, len(records), total, err)
		}
		if page == 1 && records[0].ID != reference(t, db, "posts", "1") {
			t.Fatal("pinned ordering lost")
		}
		for _, p := range records {
			if seen[p.ID] || p.InternalID == "5" {
				t.Fatal("duplicate or deleted result")
			}
			seen[p.ID] = true
		}
	}
	if len(seen) != 29 {
		t.Fatal("missing results")
	}
	for i := 1; i <= 25; i++ {
		if _, err := db.Exec(`INSERT INTO discussion_comments(id,post_id,user_id,text,created_at) VALUES(?,1,1,'Comment','2026-01-01T00:00:00Z')`, i); err != nil {
			t.Fatal(err)
		}
	}
	post := reference(t, db, "posts", "1")
	thread, total, err := s.LookupPage(ctx, post, 2)
	if err != nil || total != 25 || len(thread.Replies) != 5 || thread.Replies[0].ID != reference(t, db, "comments", "5") {
		t.Fatalf("comment page: %+v %d %v", thread, total, err)
	}
	page, err := s.CommentPage(ctx, post, reference(t, db, "comments", "1"))
	if err != nil || page != 2 {
		t.Fatalf("comment redirect page %d %v", page, err)
	}
}
