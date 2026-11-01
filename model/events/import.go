package events

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/fachschaftinformatik/web/internal/datetime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/storage"
)

type ImportedEvent struct {
	Draft                         Draft
	ExistingID, SourceURL, Status string
	PublishedAt                   time.Time
	Images                        []CoverUpload
	AlbumID                       string `json:"GalleryAlbumID"`
}

var instagramPath = regexp.MustCompile(`^/p/[A-Za-z0-9_-]+/$`)

// Import preserves captions and full-sized photographs. The source URL makes
// repeating an import idempotent without overwriting later editorial changes.
func (s *Service) Import(ctx context.Context, user int64, input ImportedEvent) (event string, created bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if err := permission(ctx, tx, user); err != nil {
		return "", false, err
	}
	u, err := url.Parse(input.SourceURL)
	if err != nil || u.Scheme != "https" || u.Host != "www.instagram.com" || !instagramPath.MatchString(u.Path) || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false, fmt.Errorf("invalid Instagram source")
	}
	if input.PublishedAt.IsZero() || len(input.Images) == 0 {
		return "", false, fmt.Errorf("source date and images required")
	}
	switch input.Status {
	case "", "cancelled", "recap", "rescheduled":
	default:
		return "", false, fmt.Errorf("invalid source status")
	}
	if fields := input.Draft.Validate(); len(fields) > 0 {
		return "", false, &ValidationError{Fields: fields}
	}
	err = tx.QueryRowContext(ctx, `SELECT CAST(id AS TEXT) FROM calendar_events WHERE source_url=?`, input.SourceURL).Scan(&event)
	if err == nil {
		ref, lookupErr := publicid.Lookup(ctx, tx, "events", event)
		return ref, false, lookupErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	oldKey := ""
	event = input.ExistingID
	if event != "" {
		event, err = resolve(ctx, tx, "events", event)
		if err != nil {
			return "", false, err
		}
		var source string
		err = tx.QueryRowContext(ctx, `SELECT cover_key,source_url FROM calendar_events WHERE id=? AND deleted_at IS NULL`, event).Scan(&oldKey, &source)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, ErrNotFound
		}
		if err != nil {
			return "", false, err
		}
		if source != "" {
			return "", false, fmt.Errorf("event already has a source")
		}
	}
	if s.storage == nil {
		return "", false, ErrCoverStorage
	}
	if err := tx.Rollback(); err != nil {
		return "", false, err
	}
	keys := []string{}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			for _, key := range keys {
				_ = s.storage.Delete(cleanup, key)
			}
		}
	}()
	upload := func(key string, data []byte, mime string) error {
		keys = append(keys, key)
		if err := s.storage.Upload(ctx, key, bytes.NewReader(data), int64(len(data)), mime); err != nil {
			return ErrCoverStorage
		}
		return nil
	}
	first := input.Images[0]
	cover, w, h, err := prepareCover(first.Data)
	if err != nil {
		return "", false, err
	}
	coverKey := "calendar/covers/" + publicid.New() + ".jpg"
	if err := upload(coverKey, cover, "image/jpeg"); err != nil {
		return "", false, err
	}
	date, err := schedule(input.Draft)
	if err != nil {
		return "", false, err
	}
	published := datetime.FormatUTC(input.PublishedAt)
	album := input.AlbumID
	newAlbum := album == ""
	albumKey := publicid.New()
	type stagedPhoto struct {
		key, mime, alt, timestamp string
		size                      int
	}
	photos := make([]stagedPhoto, 0, len(input.Images))
	// All CPU-heavy image processing and storage I/O precede the write
	// transaction. Random keys are private to this attempt until commit.
	for position, image := range input.Images {
		if strings.TrimSpace(image.Alt) == "" || utf8.RuneCountInString(image.Alt) > 500 {
			return "", false, fmt.Errorf("image description required (up to 500 characters)")
		}
		photo, _, _, err := prepareImage(image.Data, 12000)
		if err != nil {
			return "", false, err
		}
		photoKey := publicid.New()
		mime := http.DetectContentType(image.Data)
		key := storage.MediaSourceKey(photoKey)
		if err := upload(key, image.Data, mime); err != nil {
			return "", false, err
		}
		previews, err := storage.GeneratePreviews(bytes.NewReader(photo))
		if err != nil {
			return "", false, err
		}
		for size, data := range previews {
			if err := upload(storage.MediaPreviewKey(photoKey, size), data, "image/jpeg"); err != nil {
				return "", false, err
			}
		}
		if position == 0 && newAlbum {
			if err := upload(storage.AlbumCoverSourceKey(albumKey), image.Data, mime); err != nil {
				return "", false, err
			}
			for size, data := range previews {
				if err := upload(storage.AlbumCoverPreviewKey(albumKey, size), data, "image/jpeg"); err != nil {
					return "", false, err
				}
			}
		}
		photos = append(photos, stagedPhoto{key: key, mime: mime, alt: image.Alt, timestamp: datetime.FormatUTC(input.PublishedAt.Add(-time.Duration(position) * time.Second)), size: len(image.Data)})
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE calendar_events SET id=id WHERE id=?", event); err != nil {
		return "", false, err
	}
	if err := permission(ctx, tx, user); err != nil {
		return "", false, err
	}
	var duplicate string
	err = tx.QueryRowContext(ctx, "SELECT CAST(id AS TEXT) FROM calendar_events WHERE source_url=?", input.SourceURL).Scan(&duplicate)
	if err == nil {
		ref, err := publicid.Lookup(ctx, tx, "events", duplicate)
		return ref, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if event != "" {
		var currentKey, source string
		err := tx.QueryRowContext(ctx, "SELECT cover_key,source_url FROM calendar_events WHERE id=? AND deleted_at IS NULL", event).Scan(&currentKey, &source)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, ErrNotFound
		}
		if err != nil {
			return "", false, err
		}
		if source != "" || currentKey != oldKey {
			return "", false, errors.New("Der Termin wurde inzwischen geändert. Lade ihn erneut.")
		}
	}
	if newAlbum {
		internal, allocateErr := publicid.NextInteger(ctx, tx, "albums")
		if allocateErr != nil {
			return "", false, allocateErr
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO events(id,title,cover_path,created_at,updated_at) VALUES(?,?,?,?,?) RETURNING id`, internal, input.Draft.Title, storage.AlbumCoverSourceKey(albumKey), published, published).Scan(&album)
	} else {
		album, err = resolve(ctx, tx, "albums", album)
		if err != nil {
			return "", false, err
		}
		var title string
		err = tx.QueryRowContext(ctx, `SELECT title FROM events WHERE id=? AND deleted_at IS NULL`, album).Scan(&title)
	}
	if err != nil {
		return "", false, err
	}
	if input.ExistingID == "" {
		internal, allocateErr := publicid.NextInteger(ctx, tx, "events")
		if allocateErr != nil {
			return "", false, allocateErr
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO calendar_events(id,title,body,user_id,event_date,location,cover_key,cover_alt,cover_width,cover_height,source_url,source_published_at,source_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`, internal, input.Draft.Title, input.Draft.Body, user, date, input.Draft.Location, coverKey, first.Alt, w, h, input.SourceURL, published, input.Status, published, published).Scan(&event)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE calendar_events SET title=?,body=?,body_format='plain',event_date=?,location=?,cover_key=?,cover_alt=?,cover_width=?,cover_height=?,source_url=?,source_published_at=?,source_status=?,updated_at=? WHERE id=?`, input.Draft.Title, input.Draft.Body, date, input.Draft.Location, coverKey, first.Alt, w, h, input.SourceURL, published, input.Status, published, event)
	}
	if err != nil {
		return "", false, err
	}
	var firstPhoto string
	for position, photo := range photos {
		var photoID string
		internal, allocateErr := publicid.NextInteger(ctx, tx, "media")
		if allocateErr != nil {
			return "", false, allocateErr
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO media(id,event_id,user_id,title,description,access_key,mime_type,nbytes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING id`, internal, album, user, fmt.Sprintf("%s – Foto %d", input.Draft.Title, position+1), photo.alt, photo.key, photo.mime, photo.size, photo.timestamp, photo.timestamp).Scan(&photoID); err != nil {
			return "", false, err
		}
		if _, err := publicid.Ensure(ctx, tx, "media", photoID); err != nil {
			return "", false, err
		}
		if position == 0 {
			firstPhoto = photoID
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE calendar_events SET gallery_album_id=?,gallery_cover_id=? WHERE id=?`, album, firstPhoto, event); err != nil {
		return "", false, err
	}
	ref, err := publicid.Ensure(ctx, tx, "events", event)
	if err != nil {
		return "", false, err
	}
	if _, err := publicid.Ensure(ctx, tx, "albums", album); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	committed = true
	if validCoverKey(oldKey) && oldKey != coverKey {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.storage.Delete(cleanup, oldKey)
	}
	return ref, true, nil
}
