package posts

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
)

func TestPersistentPosts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "posts.db")
	if err := database.Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO users(id,email,name,password,role,active,verified,private) VALUES
	(1,'author@fsv-wh.de','Autor','unused','user',1,1,0),
	(2,'other@fsv-wh.de','Andere','unused','user',1,1,0),
	(3,'editor@fsv-wh.de','Redaktion','unused','editor',1,1,0),
	(4,'admin@fsv-wh.de','Admin','unused','admin',1,1,0),
	(5,'inactive@fsv-wh.de','Inaktiv','unused','user',0,1,0),
	(6,'unverified@fsv-wh.de','Unbestätigt','unused','user',1,0,0),
	(7,'private@fsv-wh.de','Vertraulich','unused','user',1,1,1);
	INSERT INTO discussion_posts(id,title,body,user_id,type) VALUES(203823111852785665,'Alter Beitrag','Bestehender Text.',1,'discussion')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db)
	oldRef := reference(t, db, "posts", "203823111852785665")
	old, err := s.Lookup(ctx, oldRef)
	if err != nil || old.ID != oldRef {
		t.Fatalf("compatible ID: %+v %v", old, err)
	}
	d := Draft{Title: "Neues Treffen", Body: "Ein langer Beitrag.", Type: "discussion", Tags: "Studium, Treffen, Studium"}
	rich := `<p><strong>Ein langer Beitrag.</strong></p><script>alert(1)</script><a href="javascript:alert(2)">Link</a>`
	post, err := s.Save(ctx, 1, "", d, rich)
	if err != nil {
		t.Fatal(err)
	}
	if !publicid.Valid(post) {
		t.Fatalf("noncanonical public post ID %q", post)
	}
	if _, err := s.Lookup(ctx, old.InternalID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decimal alias accepted: %v", err)
	}
	if _, err := s.Save(ctx, 1, old.InternalID, d, rich); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decimal edit alias accepted: %v", err)
	}
	for _, user := range []int64{0, 5, 6, 99} {
		if _, err = s.Save(ctx, user, "", d, rich); !errors.Is(err, ErrForbidden) {
			t.Fatalf("user %d: %v", user, err)
		}
	}
	if _, err = s.Save(ctx, 2, post, d, rich); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	for _, user := range []int64{1, 3, 4} {
		if _, err = s.Save(ctx, user, post, d, rich); err != nil {
			t.Fatal(err)
		}
	}
	d.Title = "Geändertes Treffen"
	if _, err = s.Save(ctx, 1, post, d, rich); err != nil {
		t.Fatal(err)
	}
	bad := d
	bad.Title = "x"
	if _, err = s.Save(ctx, 1, post, bad, rich); err == nil {
		t.Fatal("accepted short title")
	}
	if _, err = s.Save(ctx, 1, post, d, `<p>kurz</p>`); err == nil {
		t.Fatal("accepted short visible body")
	}
	bad = d
	bad.Type = "event"
	if _, err = s.Save(ctx, 1, post, bad, rich); err == nil {
		t.Fatal("converted discussion to event")
	}
	if _, err = s.Save(ctx, 1, "", bad, rich); err == nil {
		t.Fatal("created event as a discussion post")
	}
	bad.Type = "news"
	if _, err = s.Save(ctx, 1, "", bad, rich); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary member published announcement")
	}
	news, err := s.Save(ctx, 3, "", bad, rich)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, 1, news, d, rich); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary member changed announcement")
	}
	comment, err := s.SaveComment(ctx, 7, post, "", "Kommentar mit <script>Text</script>.")
	if err != nil {
		t.Fatal(err)
	}
	if !publicid.Valid(comment) {
		t.Fatalf("noncanonical public comment ID %q", comment)
	}
	for _, user := range []int64{0, 2, 5, 6} {
		if _, err = s.SaveComment(ctx, user, post, comment, "Ein anderer Text."); !errors.Is(err, ErrForbidden) {
			t.Fatalf("comment edit user %d: %v", user, err)
		}
	}
	if _, err = s.SaveComment(ctx, 3, post, comment, "Redaktionell bearbeiteter Kommentar."); err != nil {
		t.Fatal("editor moderation denied", err)
	}
	if _, err = s.SaveComment(ctx, 7, post, comment, "Eigener neuer Kommentar."); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveComment(ctx, 4, post, comment, "Bearbeiteter Kommentar."); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveComment(ctx, 7, post, comment, "  "); err == nil {
		t.Fatal("accepted empty comment")
	}
	if _, err = s.SaveComment(ctx, 1, oldRef, comment, "Falscher Beitrag."); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.SaveComment(ctx, 1, "999", "", "Fehlender Beitrag."); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = NewService(db)
	thread, err := s.Lookup(ctx, post)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Author != "Autor" || thread.AuthorID != 1 || thread.BodyFormat != "html" || !strings.Contains(thread.Body, "<strong>") || strings.Contains(thread.Body, "script") || strings.Contains(thread.Body, "alert") {
		t.Fatalf("persisted body and author: %+v", thread)
	}
	if thread.Title != "Geändertes Treffen" || thread.Type != "discussion" || len(thread.Tags) != 2 {
		t.Fatalf("persisted discussion: %+v", thread)
	}
	if len(thread.Replies) != 1 || thread.Comments != 1 || thread.Replies[0].ID != comment || thread.Replies[0].Author != "Anonym" || thread.Replies[0].Text != "Bearbeiteter Kommentar." {
		t.Fatalf("persisted comments: %+v", thread.Replies)
	}
	list, err := s.Find(ctx, Filter{Query: "Treffen Studium", Type: "discussion"})
	if err != nil || len(list) != 1 || list[0].ID != post {
		t.Fatalf("filtered posts: %+v %v", list, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM activities WHERE target_id=?`, thread.InternalID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("atomic activities: %d %v", count, err)
	}
	_, err = db.Exec(`UPDATE discussion_posts SET deleted_at='2026-10-17T00:00:00Z' WHERE id=?`, thread.InternalID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Lookup(ctx, post); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.SaveComment(ctx, 1, post, "", "Nicht mehr verfügbar."); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	t.Run("deleted highest keys are never reused", func(t *testing.T) {
		first, err := s.Save(ctx, 1, "", d, rich)
		if err != nil {
			t.Fatal(err)
		}
		firstThread, err := s.Lookup(ctx, first)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM discussion_posts WHERE id=?`, firstThread.InternalID); err != nil {
			t.Fatal(err)
		}
		next, err := s.Save(ctx, 1, "", d, rich)
		if err != nil {
			t.Fatal(err)
		}
		nextThread, err := s.Lookup(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
		if next == first || nextThread.InternalID == firstThread.InternalID {
			t.Fatal("deleted post identity was reused")
		}
		if _, err := s.Save(ctx, 1, next, d, rich); err != nil {
			t.Fatal("existing post update:", err)
		}
		firstComment, err := s.SaveComment(ctx, 1, next, "", "First comment")
		if err != nil {
			t.Fatal(err)
		}
		var firstInternal string
		if err := db.QueryRow(`SELECT id FROM discussion_comments WHERE post_id=?`, nextThread.InternalID).Scan(&firstInternal); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM discussion_comments WHERE id=?`, firstInternal); err != nil {
			t.Fatal(err)
		}
		nextComment, err := s.SaveComment(ctx, 1, next, "", "Next comment")
		if err != nil {
			t.Fatal(err)
		}
		var nextInternal string
		if err := db.QueryRow(`SELECT id FROM discussion_comments WHERE post_id=?`, nextThread.InternalID).Scan(&nextInternal); err != nil {
			t.Fatal(err)
		}
		if nextComment == firstComment || nextInternal == firstInternal {
			t.Fatal("deleted comment identity was reused")
		}
		if _, err := s.SaveComment(ctx, 1, next, nextComment, "Updated comment"); err != nil {
			t.Fatal("existing comment update:", err)
		}
	})
}
