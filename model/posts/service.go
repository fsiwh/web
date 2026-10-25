package posts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/content"
	"github.com/fachschaftinformatik/web/internal/urls"

	"github.com/fachschaftinformatik/web/internal/publicid"
)

var ErrForbidden = content.ErrForbidden

var ErrNotFound = errors.New("post record not found")

type ValidationError = content.ValidationError

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func permission(ctx context.Context, db querier, user, owner int64, comments bool) error {
	var role string
	err := db.QueryRowContext(ctx, `SELECT role FROM users WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL`, user).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if owner != 0 && owner != user && role != "admin" && role != "editor" {
		return ErrForbidden
	}
	return nil
}

func (s *Service) CanWrite(ctx context.Context, user, owner int64, comments bool) error {
	return permission(ctx, s.db, user, owner, comments)
}

const postSelect = `SELECT CAST(p.id AS TEXT),p.title,p.body,p.body_format,p.user_id,CASE WHEN u.private=1 OR u.deleted_at IS NOT NULL THEN 'Anonym' ELSE u.name END,p.created_at,p.updated_at,p.type,COALESCE(p.event_date,''),COALESCE(p.location,''),(SELECT COUNT(*) FROM discussion_comments c WHERE c.post_id=p.id AND c.deleted_at IS NULL),COALESCE((SELECT SUM(vote) FROM discussion_votes WHERE post_id=p.id),0),COALESCE((SELECT vote FROM discussion_votes WHERE post_id=p.id AND user_id=?),0),u.private=0 AND u.deleted_at IS NULL FROM discussion_posts p JOIN users u ON u.id=p.user_id WHERE p.deleted_at IS NULL`

func scanThread(row interface{ Scan(...any) error }) (Thread, error) {
	var t Thread
	err := row.Scan(&t.InternalID, &t.Title, &t.Body, &t.BodyFormat, &t.AuthorID, &t.Author, &t.Created, &t.Updated, &t.Type, &t.EventDate, &t.Location, &t.Comments, &t.Votes, &t.UserVote, &t.AuthorPublic)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if t.BodyFormat == "html" {
		t.Body = content.SanitizeRichText(t.Body)
	}
	return t, err
}

func (s *Service) tags(ctx context.Context, t *Thread) error {
	var err error
	t.ID, err = publicid.Lookup(ctx, s.db, "posts", t.InternalID)
	if err != nil {
		return err
	}
	t.AuthorPublicID, err = publicid.Lookup(ctx, s.db, "users", fmt.Sprint(t.AuthorID))
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT tag FROM discussion_post_tags WHERE post_id=? ORDER BY tag`, t.InternalID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return err
		}
		t.Tags = append(t.Tags, tag)
	}
	return rows.Err()
}

func (s *Service) Find(ctx context.Context, f Filter, viewer ...int64) ([]Post, error) {
	rows, err := s.db.QueryContext(ctx, postSelect+` ORDER BY p.pinned DESC,p.created_at DESC,p.id DESC`, viewerID(viewer))
	if err != nil {
		return nil, err
	}
	var threads []Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		threads = append(threads, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var result []Post
	for _, t := range threads {
		if f.AuthorID != 0 && (t.AuthorID != f.AuthorID || !t.AuthorPublic) {
			continue
		}
		if err := s.tags(ctx, &t); err != nil {
			return nil, err
		}
		if f.Type != "" && t.Type != f.Type {
			continue
		}
		text := strings.ToLower(t.Title + " " + " " + t.Body + " " + strings.Join(t.Tags, " "))
		if slices.ContainsFunc(strings.Fields(strings.ToLower(f.Query)), func(term string) bool { return !strings.Contains(text, term) }) {
			continue
		}
		result = append(result, t.Post)
	}
	return result, nil
}

func (s *Service) Lookup(ctx context.Context, post string, viewer ...int64) (Thread, error) {
	t, _, err := s.LookupPage(ctx, post, 1, viewer...)
	return t, err
}

func (s *Service) LookupPage(ctx context.Context, post string, page int, viewer ...int64) (Thread, int, error) {
	post, err := resolve(ctx, s.db, "posts", post)
	if err != nil {
		return Thread{}, 0, err
	}
	t, err := scanThread(s.db.QueryRowContext(ctx, postSelect+` AND p.id=?`, viewerID(viewer), post))
	if err != nil {
		return t, 0, err
	}
	if err = s.tags(ctx, &t); err != nil {
		return t, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM discussion_comments WHERE post_id=? AND deleted_at IS NULL`, post).Scan(&total); err != nil {
		return t, 0, err
	}
	page = max(1, min(page, max(1, (total+19)/20)))
	rows, err := s.db.QueryContext(ctx, `SELECT CAST(c.id AS TEXT),c.user_id,CASE WHEN u.private=1 OR u.deleted_at IS NOT NULL THEN 'Anonym' ELSE u.name END,c.text,c.created_at,c.updated_at,COALESCE((SELECT SUM(vote) FROM discussion_comment_votes WHERE comment_id=c.id),0),COALESCE((SELECT vote FROM discussion_comment_votes WHERE comment_id=c.id AND user_id=?),0),u.private=0 AND u.deleted_at IS NULL FROM discussion_comments c JOIN users u ON u.id=c.user_id WHERE c.post_id=? AND c.deleted_at IS NULL ORDER BY c.created_at DESC,c.id DESC LIMIT 20 OFFSET ?`, viewerID(viewer), post, (page-1)*20)
	if err != nil {
		return t, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Comment
		if err = rows.Scan(&c.InternalID, &c.AuthorID, &c.Author, &c.Text, &c.Created, &c.Updated, &c.Votes, &c.UserVote, &c.AuthorPublic); err != nil {
			return t, 0, err
		}
		t.Replies = append(t.Replies, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return t, 0, err
	}
	for i := range t.Replies {
		c := &t.Replies[i]
		c.ID, err = publicid.Lookup(ctx, s.db, "comments", c.InternalID)
		if err != nil {
			return t, 0, err
		}
		c.AuthorPublicID, err = publicid.Lookup(ctx, s.db, "users", fmt.Sprint(c.AuthorID))
		if err != nil {
			return t, 0, err
		}
	}
	return t, total, nil
}

// Save persists sanitized HTML, event details and tags atomically. An empty post creates a new record.
func (s *Service) Save(ctx context.Context, user int64, post string, d Draft, rich string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var owner int64
	var oldType string
	if post != "" {
		post, err = resolve(ctx, tx, "posts", post)
		if err != nil {
			return "", err
		}
		if err = tx.QueryRowContext(ctx, `SELECT user_id,type FROM discussion_posts WHERE id=? AND deleted_at IS NULL`, post).Scan(&owner, &oldType); errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		} else if err != nil {
			return "", err
		}
	}
	if err = permission(ctx, tx, user, owner, false); err != nil {
		return "", err
	}
	if d.Type == "event" || oldType == "event" {
		return "", &ValidationError{Fields: []FieldError{{Field: "body", Message: "Veranstaltungen werden im Veranstaltungsbereich verwaltet."}}}
	}
	if d.Type == "news" || oldType == "news" {
		if err := editorialPermission(ctx, tx, user); err != nil {
			return "", err
		}
	}
	d.Title = strings.TrimSpace(d.Title)
	body, format := d.Body, "plain"
	if rich != "" {
		body = content.SanitizeRichText(rich)
		format = "html"
		d.Body = content.RichTextVisible(body)
	}
	if fields := d.Validate(); len(fields) > 0 {
		return "", &ValidationError{Fields: fields}
	}
	event := ""
	if post == "" {
		internal, allocateErr := publicid.NextInteger(ctx, tx, "posts")
		if allocateErr != nil {
			return "", allocateErr
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO discussion_posts(id,title,body,body_format,user_id,type,event_date,location) VALUES(?,?,?,?,?,?,?,?) RETURNING id`, internal, d.Title, body, format, user, d.Type, event, strings.TrimSpace(d.Location)).Scan(&post)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO activities(user_id,type,target_id,target_name) VALUES(?,'POST_CREATED',?,?)`, user, post, d.Title)
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE discussion_posts SET title=?,body=?,body_format=?,type=?,event_date=?,location=? WHERE id=?`, d.Title, body, format, d.Type, event, strings.TrimSpace(d.Location), post)
	}
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM discussion_post_tags WHERE post_id=?`, post); err != nil {
		return "", err
	}
	for _, tag := range strings.Split(d.Tags, ",") {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO discussion_post_tags(post_id,tag) VALUES(?,?)`, post, tag); err != nil {
				return "", err
			}
		}
	}
	ref, err := publicid.Ensure(ctx, tx, "posts", post)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	eventName := "content.discussion_saved"
	if d.Type == "news" {
		eventName = "content.news_saved"
	}
	audit.Content(ctx, eventName, d.Title, urls.Post(ref))
	return ref, nil
}

func (s *Service) SaveComment(ctx context.Context, user int64, post, comment, text string) (string, error) {
	edited := comment != ""
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	post, err = resolve(ctx, tx, "posts", post)
	if err != nil {
		return "", err
	}
	if comment != "" {
		comment, err = resolve(ctx, tx, "comments", comment)
		if err != nil {
			return "", err
		}
	}
	var title string
	if err = tx.QueryRowContext(ctx, `SELECT title FROM discussion_posts WHERE id=? AND deleted_at IS NULL`, post).Scan(&title); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	var owner int64
	if comment != "" {
		if err = tx.QueryRowContext(ctx, `SELECT user_id FROM discussion_comments WHERE id=? AND post_id=? AND deleted_at IS NULL`, comment, post).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		} else if err != nil {
			return "", err
		}
	}
	if err = permission(ctx, tx, user, owner, true); err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n < 1 || n > 10000 {
		return "", &ValidationError{Fields: []FieldError{{Field: "text", Message: "Der Kommentar muss zwischen 1 und 10.000 Zeichen lang sein."}}}
	}
	if comment == "" {
		internal, allocateErr := publicid.NextInteger(ctx, tx, "comments")
		if allocateErr != nil {
			return "", allocateErr
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO discussion_comments(id,post_id,user_id,text) VALUES(?,?,?,?) RETURNING id`, internal, post, user, text).Scan(&comment)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO activities(user_id,type,target_id,target_name) VALUES(?,'COMMENT_ADDED',?,?)`, user, post, title)
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE discussion_comments SET text=? WHERE id=? AND post_id=?`, text, comment, post)
	}
	if err != nil {
		return "", err
	}
	ref, err := publicid.Ensure(ctx, tx, "comments", comment)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if edited {
		postRef, err := publicid.Lookup(ctx, s.db, "posts", post)
		if err == nil {
			audit.Content(ctx, "content.comment_edited", title, urls.Post(postRef))
		}
	}
	return ref, nil
}

// CommentPage keeps edit/vote redirects on the page containing the comment.
func (s *Service) CommentPage(ctx context.Context, post, comment string) (int, error) {
	var resolveErr error
	post, resolveErr = resolve(ctx, s.db, "posts", post)
	if resolveErr != nil {
		return 0, resolveErr
	}
	comment, resolveErr = resolve(ctx, s.db, "comments", comment)
	if resolveErr != nil {
		return 0, resolveErr
	}
	var before int
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM discussion_comments preceding WHERE preceding.post_id=c.post_id AND preceding.deleted_at IS NULL AND (preceding.created_at>c.created_at OR (preceding.created_at=c.created_at AND preceding.id>c.id))) FROM discussion_comments c JOIN discussion_posts p ON p.id=c.post_id AND p.deleted_at IS NULL WHERE c.id=? AND c.post_id=? AND c.deleted_at IS NULL`, comment, post).Scan(&before)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return before/20 + 1, err
}

// FindPage applies filters before limiting results, including tag searches.
func (s *Service) FindPage(ctx context.Context, f Filter, page, size int, viewer ...int64) ([]Post, int, error) {
	size = min(100, max(1, size))
	where := ""
	args := []any{}
	if f.Type != "" {
		where += " AND p.type=?"
		args = append(args, f.Type)
	}
	if f.AuthorID != 0 {
		where += " AND p.user_id=? AND u.private=0 AND u.deleted_at IS NULL"
		args = append(args, f.AuthorID)
	}
	for _, term := range strings.Fields(strings.ToLower(f.Query)) {
		where += ` AND (instr(lower(p.title||' '||p.body),?)>0 OR EXISTS(SELECT 1 FROM discussion_post_tags t WHERE t.post_id=p.id AND instr(lower(t.tag),?)>0))`
		args = append(args, term, term)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM discussion_posts p JOIN users u ON u.id=p.user_id WHERE p.deleted_at IS NULL`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	page = max(1, min(page, max(1, (total+size-1)/size)))
	params := append([]any{viewerID(viewer)}, args...)
	params = append(params, size, (page-1)*size)
	rows, err := s.db.QueryContext(ctx, postSelect+where+` ORDER BY p.pinned DESC,p.created_at DESC,p.id DESC LIMIT ? OFFSET ?`, params...)
	if err != nil {
		return nil, 0, err
	}
	var threads []Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		threads = append(threads, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	result := make([]Post, 0, len(threads))
	for _, t := range threads {
		if err := s.tags(ctx, &t); err != nil {
			return nil, 0, err
		}
		result = append(result, t.Post)
	}
	return result, total, nil
}
