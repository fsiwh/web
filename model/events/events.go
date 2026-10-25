package events

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/urls"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fachschaftinformatik/web/internal/content"
	"github.com/fachschaftinformatik/web/internal/datetime"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/storage"
)

var ErrNotFound = errors.New("event not found")

var ErrForbidden = content.ErrForbidden

type ValidationError = content.ValidationError

type FieldError = content.FieldError

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Service struct {
	db      *sql.DB
	storage storage.Provider
}

func NewService(db *sql.DB, storage storage.Provider) *Service {
	return &Service{db: db, storage: storage}
}

type Event struct {
	InternalID, LegacyPostPublicID, AlbumPublicID, AlbumCoverPublicID  string
	ID, Title, Body, BodyFormat, EventDate, Location, Created, Updated string
	CoverKey, CoverAlt                                                 string
	CoverWidth, CoverHeight                                            int
	LegacyPostID                                                       sql.NullInt64
	SourceURL, SourcePublishedAt, SourceStatus                         string
	AlbumID                                                            sql.NullInt64 `json:"GalleryAlbumID"`
	AlbumCoverID                                                       sql.NullInt64 `json:"GalleryCoverID"`
}

type Draft struct{ Title, Body, Date, Time, Location string }

func (d Draft) Validate() []FieldError {
	return append(content.ValidateText(d.Title, d.Body), content.ValidateSchedule(d.Date, d.Time)...)
}

const selectEvent = `SELECT CAST(id AS TEXT),title,body,body_format,event_date,location,created_at,updated_at,cover_key,cover_alt,cover_width,cover_height,legacy_post_id,source_url,source_published_at,source_status,
CASE WHEN EXISTS(SELECT 1 FROM events album WHERE album.id=gallery_album_id AND album.deleted_at IS NULL)
AND EXISTS(SELECT 1 FROM media photos WHERE photos.event_id=gallery_album_id AND photos.deleted_at IS NULL AND photos.mime_type IN ('image/jpeg','image/png','image/gif','image/webp','image/bmp'))
THEN gallery_album_id END,gallery_cover_id FROM calendar_events WHERE deleted_at IS NULL`

func scan(row interface{ Scan(...any) error }) (Event, error) {
	var e Event
	err := row.Scan(&e.InternalID, &e.Title, &e.Body, &e.BodyFormat, &e.EventDate, &e.Location, &e.Created, &e.Updated, &e.CoverKey, &e.CoverAlt, &e.CoverWidth, &e.CoverHeight, &e.LegacyPostID, &e.SourceURL, &e.SourcePublishedAt, &e.SourceStatus, &e.AlbumID, &e.AlbumCoverID)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if e.BodyFormat == "html" {
		e.Body = content.SanitizeRichText(e.Body)
	}
	return e, err
}

func (s *Service) Find(ctx context.Context) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, selectEvent+` ORDER BY event_date,created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Event
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range result {
		if err := s.references(ctx, &result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Service) Lookup(ctx context.Context, event string) (Event, error) {
	internal, err := resolve(ctx, s.db, "events", event)
	if err != nil {
		return Event{}, err
	}
	e, err := scan(s.db.QueryRowContext(ctx, selectEvent+` AND id=?`, internal))
	if err == nil {
		err = s.references(ctx, &e)
	}
	return e, err
}

// ForPost resolves a calendar event through its actual linked post, not by
// assuming that the two independently addressable records have equal IDs.
func (s *Service) ForPost(ctx context.Context, post string) (Event, error) {
	internal, err := resolve(ctx, s.db, "posts", post)
	if err != nil {
		return Event{}, err
	}
	e, err := scan(s.db.QueryRowContext(ctx, selectEvent+` AND legacy_post_id=?`, internal))
	if err == nil {
		err = s.references(ctx, &e)
	}
	return e, err
}

func (e Event) SortDate() string {
	if e.EventDate != "" {
		return e.EventDate
	}
	return e.SourcePublishedAt
}

func (s *Service) Save(ctx context.Context, user int64, event string, d Draft, rich string, cover *CoverUpload) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := permission(ctx, tx, user); err != nil {
		return "", err
	}
	var oldKey, coverAlt string
	var width, height int
	if event != "" {
		event, err = resolve(ctx, tx, "events", event)
		if err != nil {
			return "", err
		}
		err = tx.QueryRowContext(ctx, `SELECT cover_key,cover_alt,cover_width,cover_height FROM calendar_events WHERE id=? AND deleted_at IS NULL`, event).Scan(&oldKey, &coverAlt, &width, &height)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", err
		}
	}
	body, format := d.Body, "plain"
	if rich != "" {
		body, format = content.SanitizeRichText(rich), "html"
		d.Body = content.RichTextVisible(body)
	}
	if fields := d.Validate(); len(fields) > 0 {
		return "", &ValidationError{Fields: fields}
	}
	coverKey := oldKey
	newKey := ""
	committed := false
	defer func() {
		if !committed && newKey != "" {
			_ = s.storage.Delete(context.WithoutCancel(ctx), newKey)
		}
	}()
	if cover != nil {
		if cover.Remove && len(cover.Data) > 0 {
			return "", &ValidationError{Fields: []FieldError{{Field: "cover", Message: "Wähle entweder ein neues Titelbild oder entferne das bisherige."}}}
		}
		if cover.Remove {
			coverKey, coverAlt, width, height = "", "", 0, 0
		}
		if len(cover.Data) > 0 {
			if strings.TrimSpace(cover.Alt) == "" || utf8.RuneCountInString(cover.Alt) > 500 {
				return "", &ValidationError{Fields: []FieldError{{Field: "cover_alt", Message: "Beschreibe das Titelbild mit höchstens 500 Zeichen."}}}
			}
			photo, w, h, err := prepareCover(cover.Data)
			if err != nil {
				return "", err
			}
			if s.storage == nil {
				return "", ErrCoverStorage
			}
			newKey = "calendar/covers/" + publicid.New() + ".jpg"
			if err := s.storage.Upload(ctx, newKey, bytes.NewReader(photo), int64(len(photo)), "image/jpeg"); err != nil {
				return "", ErrCoverStorage
			}
			coverKey, width, height = newKey, w, h
		}
		if coverKey != "" && !cover.Remove {
			if strings.TrimSpace(cover.Alt) == "" || utf8.RuneCountInString(cover.Alt) > 500 {
				return "", &ValidationError{Fields: []FieldError{{Field: "cover_alt", Message: "Beschreibe das Titelbild mit höchstens 500 Zeichen."}}}
			}
			coverAlt = strings.TrimSpace(cover.Alt)
		}
	}
	date, err := schedule(d)
	if err != nil {
		return "", err
	}
	if event == "" {
		internal, allocateErr := publicid.NextInteger(ctx, tx, "events")
		if allocateErr != nil {
			return "", allocateErr
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO calendar_events(id,title,body,body_format,event_date,location,user_id,cover_key,cover_alt,cover_width,cover_height) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING id`, internal, strings.TrimSpace(d.Title), body, format, date, strings.TrimSpace(d.Location), user, coverKey, coverAlt, width, height).Scan(&event)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE calendar_events SET title=?,body=?,body_format=?,event_date=?,location=?,cover_key=?,cover_alt=?,cover_width=?,cover_height=?,updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id=?`, strings.TrimSpace(d.Title), body, format, date, strings.TrimSpace(d.Location), coverKey, coverAlt, width, height, event)
	}
	if err != nil {
		return "", err
	}
	if oldKey != coverKey {
		if _, err = tx.ExecContext(ctx, `UPDATE calendar_events SET gallery_cover_id=NULL WHERE id=?`, event); err != nil {
			return "", err
		}
	}
	ref, err := publicid.Ensure(ctx, tx, "events", event)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	committed = true
	audit.Content(ctx, "content.event_saved", strings.TrimSpace(d.Title), urls.Event(ref))
	if validCoverKey(oldKey) && oldKey != coverKey && s.storage != nil {
		_ = s.storage.Delete(context.WithoutCancel(ctx), oldKey)
	}
	return ref, nil
}

// FindPage returns upcoming events first, then history, with stable tie-breaks.
// The home page requests only upcoming records with a smaller limit.
func (s *Service) FindPage(ctx context.Context, now time.Time, page, size int, upcomingOnly bool) ([]Event, int, error) {
	now = now.In(datetime.Berlin)
	today := now.Format("2006-01-02")
	midnight := datetime.FormatUTC(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, datetime.Berlin))
	date := `COALESCE(NULLIF(event_date,''),NULLIF(source_published_at,''),'')`
	upcoming := `(source_status!='cancelled' AND (` + date + `='' OR CASE WHEN length(` + date + `)=10 THEN ` + date + `>=? ELSE ` + date + `>=? END))`
	where := ""
	args := []any{}
	if upcomingOnly {
		where = " AND " + upcoming
		args = append(args, today, midnight)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendar_events WHERE deleted_at IS NULL`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	size = min(100, max(1, size))
	page = max(1, min(page, max(1, (total+size-1)/size)))
	args = append(args, today, midnight, today, midnight, size, (page-1)*size)
	rows, err := s.db.QueryContext(ctx, selectEvent+where+` ORDER BY CASE WHEN `+upcoming+` THEN 0 ELSE 1 END,CASE WHEN `+upcoming+` THEN CASE WHEN `+date+`='' THEN '9999' ELSE `+date+` END END ASC,`+date+` DESC,id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var result []Event
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for i := range result {
		if err := s.references(ctx, &result[i]); err != nil {
			return nil, 0, err
		}
	}
	return result, total, nil
}

// Group classifies and orders events for the calendar and upcoming lists.
func Group(records []Event, now time.Time) (upcoming, past []Event) {
	now = now.In(datetime.Berlin)
	for _, event := range records {
		date := event.SortDate()
		if stamp, err := time.Parse(time.RFC3339, date); err == nil {
			date = stamp.In(datetime.Berlin).Format(time.DateOnly)
		}
		if event.SourceStatus == "cancelled" || len(date) >= 10 && date[:10] < now.Format("2006-01-02") {
			past = append(past, event)
		} else {
			upcoming = append(upcoming, event)
		}
	}
	sort.SliceStable(upcoming, func(i, j int) bool {
		a, b := upcoming[i].SortDate(), upcoming[j].SortDate()
		if a == "" {
			return false
		}
		if b == "" {
			return true
		}
		return a < b
	})
	sort.SliceStable(past, func(i, j int) bool { return past[i].SortDate() > past[j].SortDate() })
	return upcoming, past
}
