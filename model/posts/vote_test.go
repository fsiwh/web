package posts

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"testing"

	"github.com/fachschaftinformatik/web/internal/database"
)

func TestVotesPersistWithoutChangingOtherUsersAndRespectVisibility(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "votes.db")
	if err := database.Migrate(path, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	db, err := database.NewConnection(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO users(id,email,name,password,active,verified) VALUES
	(1,'one@council.example.invalid','One','unused',1,1),(2,'two@council.example.invalid','Two','unused',1,1),
	(3,'inactive@council.example.invalid','Inactive','unused',0,1),(4,'unverified@council.example.invalid','Unverified','unused',1,0);
	INSERT INTO discussion_posts(id,title,body,user_id,type,deleted_at) VALUES
	(10,'Public post','Body',1,'discussion',NULL),(20,'Other post','Body',1,'discussion',NULL),(30,'Deleted post','Body',1,'discussion','2026-01-01T00:00:00Z');
	INSERT INTO discussion_comments(id,post_id,user_id,text,deleted_at) VALUES
	(100,10,1,'Public',NULL),(200,20,1,'Other post',NULL),(300,10,1,'Deleted','2026-01-01T00:00:00Z');
	INSERT INTO discussion_votes VALUES(10,2,1);
	INSERT INTO discussion_comment_votes VALUES(100,2,-1);`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db)
	post := reference(t, db, "posts", "10")
	comment := reference(t, db, "comments", "100")
	if err := s.Vote(ctx, 1, "10", "", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decimal post vote alias: %v", err)
	}
	if err := s.Vote(ctx, 1, post, "100", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decimal comment vote alias: %v", err)
	}
	for _, value := range []int{1, 1, -1, 0, 0} {
		if err := s.Vote(ctx, 1, post, "", value); err != nil {
			t.Fatal(err)
		}
		if err := s.Vote(ctx, 1, post, comment, value); err != nil {
			t.Fatal(err)
		}
		thread, err := NewService(db).Lookup(ctx, post, 1)
		if err != nil || thread.Votes != 1+value || thread.UserVote != value || thread.Replies[0].Votes != -1+value || thread.Replies[0].UserVote != value {
			t.Fatalf("value %d: %+v %v", value, thread, err)
		}
		other, err := s.Lookup(ctx, post, 2)
		if err != nil || other.UserVote != 1 || other.Replies[0].UserVote != -1 {
			t.Fatalf("other viewer: %+v %v", other, err)
		}
		list, err := s.Find(ctx, Filter{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range list {
			if p.ID == post && (p.Votes != 1+value || p.UserVote != value) {
				t.Fatalf("list: %+v", p)
			}
		}
	}
	for _, user := range []int64{0, 3, 4, 99} {
		if err := s.Vote(ctx, user, post, "", 1); !errors.Is(err, ErrForbidden) {
			t.Fatalf("user %d: %v", user, err)
		}
	}
	for _, target := range []struct{ post, comment string }{{"30", ""}, {"999", ""}, {"10", "200"}, {"10", "300"}, {"10", "999"}} {
		if target.post != "999" {
			target.post = reference(t, db, "posts", target.post)
		}
		if target.comment != "" && target.comment != "999" {
			target.comment = reference(t, db, "comments", target.comment)
		}
		if err := s.Vote(ctx, 1, target.post, target.comment, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("target %+v: %v", target, err)
		}
	}
	if err := s.Vote(ctx, 1, post, "", 2); err == nil {
		t.Fatal("invalid value accepted")
	}
	if _, err := db.Exec(`UPDATE users SET deleted_at='2026-01-01T00:00:00Z' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Vote(ctx, 1, post, "", 1); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}
