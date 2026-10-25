package media

import (
	"context"
	"database/sql"
	"errors"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/content"
)

var ErrForbidden = content.ErrForbidden

func galleryPermission(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, user int64) error {
	var allowed int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL AND role IN ('admin','editor')`, user).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func (s *Service) CanManage(ctx context.Context, user int64) error {
	return galleryPermission(ctx, s.db, user)
}

type GalleryItem struct {
	Image
	AlbumTitle string
	Visible    bool
}

// GalleryPage lists all photographs and their independent homepage selection.
func (s *Service) GalleryPage(ctx context.Context, user int64, page, size int) ([]GalleryItem, int, error) {
	if err := s.CanManage(ctx, user); err != nil {
		return nil, 0, err
	}
	const eligible = `m.deleted_at IS NULL AND e.deleted_at IS NULL AND m.mime_type IN ` + imageTypes
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media m JOIN events e ON e.id=m.event_id WHERE `+eligible).Scan(&total); err != nil {
		return nil, 0, err
	}
	size = min(100, max(1, size))
	page = max(1, min(page, max(1, (total+size-1)/size)))
	rows, err := s.db.QueryContext(ctx, `SELECT CAST(m.id AS TEXT),COALESCE(m.title,''),COALESCE(m.description,''),e.title,m.homepage_visible FROM media m JOIN events e ON e.id=m.event_id WHERE `+eligible+` ORDER BY e.created_at DESC,e.id DESC,m.created_at DESC,m.id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []GalleryItem
	for rows.Next() {
		var item GalleryItem
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.AlbumTitle, &item.Visible); err != nil {
			return nil, 0, err
		}
		item.Image = albumPhoto(item.ID, item.Title, item.Description, (page-1)*size+len(items)+1, item.AlbumTitle)
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		if err := s.imageReference(ctx, &items[i].Image); err != nil {
			return nil, 0, err
		}
	}
	return items, total, nil
}

func (s *Service) SetHomepageVisibility(ctx context.Context, user int64, value string, visible bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := galleryPermission(ctx, tx, user); err != nil {
		return err
	}
	id, err := resolve(ctx, tx, "media", value)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE media SET homepage_visible=? WHERE id=? AND deleted_at IS NULL AND mime_type IN `+imageTypes+` AND event_id IN (SELECT id FROM events WHERE deleted_at IS NULL)`, visible, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	audit.Content(ctx, "content.photo_visibility", "Fotos", "/media/manage")
	return nil
}
