package meetings

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/fachschaftinformatik/web/internal/content"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/storage"
)

// NewStore uses the migrated database and a private blob bucket.
func NewStore(db *sql.DB, provider storage.Provider) (*Store, error) {
	if db == nil || provider == nil {
		return nil, errors.New("Datenbank und Dateispeicher fehlen.")
	}
	if _, err := db.ExecContext(context.Background(), "SELECT id FROM meeting_publications LIMIT 0"); err != nil {
		return nil, err
	}
	files, err := historicalFiles(context.Background(), db)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, blobs: provider, historicalFiles: files}, nil
}

func (s *Store) Save(p Publication) (err error) {
	return s.SaveContext(context.Background(), p)
}

// Callers may impose a shorter deadline, including client disconnection.
const operationTimeout = 30 * time.Second

func publicationPermission(ctx context.Context, db database.DBTX, owner string) error {
	id, err := strconv.ParseInt(owner, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != owner {
		return content.ErrForbidden
	}
	var allowed bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND active=1 AND verified=1 AND deleted_at IS NULL AND role IN ('admin','editor'))", id).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return content.ErrForbidden
	}
	return nil
}

func (s *Store) SaveContext(ctx context.Context, p Publication) (err error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	ownerID, ownerErr := strconv.ParseInt(p.Owner, 10, 64)
	if p.ID == "" || ownerErr != nil || ownerID <= 0 || strconv.FormatInt(ownerID, 10) != p.Owner || len(p.Data) > MaxPDFBytes {
		return errors.New("Der Entwurf konnte nicht gespeichert werden.")
	}
	if err := publicationPermission(ctx, s.db, p.Owner); err != nil {
		return err
	}
	var owner string
	var oldKey, oldDigest string
	var published bool
	err = s.db.QueryRowContext(ctx, "SELECT owner,published,blob_key,sha256 FROM meeting_publications WHERE id=?", p.ID).Scan(&owner, &published, &oldKey, &oldDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (owner != p.Owner || published) {
		return errors.New("Dieser Entwurf kann nicht geändert werden.")
	}
	hash := sha256.Sum256(p.Data)
	digest := hex.EncodeToString(hash[:])
	key := oldKey
	upload := oldKey == "" || oldDigest != digest
	if upload {
		key = "meetings/" + rand.Text()
	}
	// Upload errors may leave partial objects. Always attempt compensation.
	committed := false
	var tx *sql.Tx
	defer func() {
		if !committed && upload {
			if tx != nil {
				tx.Rollback()
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if cleanup := s.blobs.Delete(cleanupCtx, key); cleanup != nil {
				err = errors.Join(err, fmt.Errorf("Datei konnte nicht entfernt werden: %w", cleanup))
				_, queueErr := s.db.ExecContext(cleanupCtx, "INSERT OR IGNORE INTO meeting_blob_cleanup(blob_key) VALUES(?)", key)
				err = errors.Join(err, queueErr)
			}
		}
	}()
	if upload {
		if err = s.blobs.Upload(ctx, key, bytes.NewReader(p.Data), int64(len(p.Data)), "application/pdf"); err != nil {
			return err
		}
	}
	// Objects have unique immutable keys. Upload first, then recheck ownership,
	// publication and the previous key under a short writer transaction. A stale
	// save must not resurrect a retired key or overwrite a concurrently saved PDF.
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE meeting_publications SET id=id WHERE id=?", p.ID); err != nil {
		return err
	}
	if err := publicationPermission(ctx, tx, p.Owner); err != nil {
		return err
	}
	var currentKey string
	err = tx.QueryRowContext(ctx, "SELECT owner,published,blob_key FROM meeting_publications WHERE id=?", p.ID).Scan(&owner, &published, &currentKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (owner != p.Owner || published) {
		return errors.New("Dieser Entwurf kann nicht geändert werden.")
	}
	if currentKey != oldKey {
		return errors.New("Der Entwurf wurde inzwischen geändert. Lade ihn erneut.")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO meeting_publications(id,owner,date,number,kind,filename,blob_key,bytes,sha256)
	VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET date=excluded.date,number=excluded.number,kind=excluded.kind,
	filename=excluded.filename,blob_key=excluded.blob_key,bytes=excluded.bytes,sha256=excluded.sha256`, p.ID, p.Owner, p.Date, p.Number, p.Kind, p.Filename, key, len(p.Data), digest)
	if err != nil {
		return err
	}
	if _, err = publicid.Ensure(ctx, tx, "publications", p.ID); err != nil {
		return err
	}
	if upload && oldKey != "" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO meeting_blob_cleanup(blob_key) VALUES(?)", oldKey); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	// Deletion failures remain durably queued. The saved draft is already committed.
	_ = s.CleanupBlobs(ctx)
	return nil
}

type publicationRow interface{ Scan(...any) error }

func (s *Store) readPublication(ctx context.Context, row publicationRow) (Publication, bool, error) {
	var p Publication
	var key, digest string
	var size int64
	err := row.Scan(&p.ID, &p.Owner, &p.Date, &p.Number, &p.Kind, &p.Filename, &key, &size, &digest, &p.Published)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	object, err := s.blobs.GetObject(ctx, key)
	if err != nil {
		return p, false, err
	}
	p.Data, err = io.ReadAll(io.LimitReader(object, MaxPDFBytes+1))
	err = errors.Join(err, object.Close())
	if err != nil {
		return p, false, err
	}
	hash := sha256.Sum256(p.Data)
	if int64(len(p.Data)) != size || hex.EncodeToString(hash[:]) != digest {
		return p, false, errors.New("Die gespeicherte Datei ist beschädigt.")
	}
	return p, true, nil
}

const publicationColumns = "id,owner,date,number,kind,filename,blob_key,bytes,sha256,published"

func (s *Store) GetWithError(id string) (Publication, bool, error) {
	return s.GetContext(context.Background(), id)
}

func (s *Store) GetContext(ctx context.Context, id string) (Publication, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	// Download an immutable snapshot without occupying the SQL pool. A concurrent
	// replacement may retire the selected key before GET opens it; one retry then
	// loads the current snapshot. Never combine bytes with another row's metadata.
	p, ok, err := s.readPublication(ctx, s.db.QueryRowContext(ctx, "SELECT "+publicationColumns+" FROM meeting_publications WHERE id=?", id))
	if err != nil && ctx.Err() == nil {
		p, ok, err = s.readPublication(ctx, s.db.QueryRowContext(ctx, "SELECT "+publicationColumns+" FROM meeting_publications WHERE id=?", id))
	}
	if err == nil && ok {
		p.PublicID, err = publicid.Ensure(ctx, s.db, "publications", p.ID)
	}
	return p, ok, err
}

func (s *Store) ResolvePublicID(ctx context.Context, value string) (string, error) {
	if !publicid.Valid(value) {
		return "", sql.ErrNoRows
	}
	return publicid.Resolve(ctx, s.db, "publications", value)
}

func (s *Store) PublicID(ctx context.Context, internal string) (string, error) {
	return publicid.Lookup(ctx, s.db, "publications", internal)
}

func (s *Store) Publish(id, owner string) error {
	return s.PublishContext(context.Background(), id, owner)
}

func (s *Store) PublishContext(ctx context.Context, id, owner string) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := publicationPermission(ctx, s.db, owner); err != nil {
		return err
	}
	var owns bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meeting_publications WHERE id=? AND owner=?)", id, owner).Scan(&owns); err != nil {
		return err
	}
	if !owns {
		return errors.New("Dieser Entwurf wurde nicht gefunden.")
	}
	// Verify the immutable bytes outside the transaction. Comparing all metadata
	// again under the writer lock prevents publishing a concurrently edited draft.
	p, ok, err := s.GetContext(ctx, id)
	if err != nil {
		return err
	}
	if !ok || p.Owner != owner {
		return errors.New("Dieser Entwurf wurde nicht gefunden.")
	}
	if len(ValidatePublication(p)) != 0 {
		return errors.New("Prüfe die Angaben und die Datei.")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE meeting_publications SET id=id WHERE id=?", id); err != nil {
		return err
	}
	if err := publicationPermission(ctx, tx, owner); err != nil {
		return err
	}
	var current Publication
	var checksum string
	var size int64
	err = tx.QueryRowContext(ctx, "SELECT id,owner,date,number,kind,filename,bytes,sha256,published FROM meeting_publications WHERE id=?", id).Scan(&current.ID, &current.Owner, &current.Date, &current.Number, &current.Kind, &current.Filename, &size, &checksum, &current.Published)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(p.Data)
	if current.Owner != owner || current.Date != p.Date || current.Number != p.Number || current.Kind != p.Kind || current.Filename != p.Filename || size != int64(len(p.Data)) || checksum != hex.EncodeToString(hash[:]) {
		return errors.New("Der Entwurf wurde inzwischen geändert. Lade ihn erneut.")
	}
	if current.Published {
		return nil
	}
	date, _ := time.Parse("2006-01-02", p.Date)
	conflict := errors.New("Datum und Sitzungsnummer stimmen nicht mit der vorhandenen Sitzung überein.")
	historical, err := s.historical(ctx, tx)
	if err != nil {
		return err
	}
	for _, m := range historical {
		if m.Year == date.Year() && (m.Date == p.Date && m.Number != p.Number || m.Number == p.Number && m.Date != p.Date) {
			return conflict
		}
	}
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT date FROM published_meetings WHERE year=? AND number=?", date.Year(), p.Number).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existing != p.Date {
		return conflict
	}
	var number int
	err = tx.QueryRowContext(ctx, "SELECT number FROM published_meetings WHERE date=?", p.Date).Scan(&number)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && number != p.Number {
		return conflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO published_meetings(year,number,date) VALUES(?,?,?) ON CONFLICT DO NOTHING", date.Year(), p.Number, p.Date); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE meeting_publications SET published=1 WHERE id=? AND owner=?", id, owner); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AllWithError() ([]Meeting, error) {
	return s.AllContext(context.Background())
}

func (s *Store) AllContext(ctx context.Context) ([]Meeting, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	records, err := s.historical(ctx, s.db)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,date,number,kind,bytes FROM meeting_publications WHERE published=1 ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var publications []Publication
	var sizes []int
	for rows.Next() {
		var p Publication
		var size int
		if err := rows.Scan(&p.ID, &p.Date, &p.Number, &p.Kind, &size); err != nil {
			return nil, err
		}
		publications = append(publications, p)
		sizes = append(sizes, size)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i, p := range publications {
		p.PublicID, err = publicid.Ensure(ctx, s.db, "publications", p.ID)
		if err != nil {
			return nil, err
		}
		records = appendPublication(records, p, sizes[i])
	}
	sortMeetings(records)
	return records, nil
}

// CleanupBlobs retries at most 16 obsolete draft objects. Failed deletions remain
// queued across restarts. Saves also invoke it after committing their changes.
func (s *Store) CleanupBlobs(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	// Retired keys are never reused: saves compare their pre-upload key inside
	// the write transaction. Deleting these objects needs no SQL transaction.
	rows, err := s.db.QueryContext(ctx, `SELECT blob_key FROM meeting_blob_cleanup
	WHERE NOT EXISTS (SELECT 1 FROM meeting_publications p WHERE p.blob_key=meeting_blob_cleanup.blob_key)
	ORDER BY attempts,blob_key LIMIT 16`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	var failures error
	for _, key := range keys {
		if err := s.blobs.Delete(ctx, key); err != nil {
			failures = errors.Join(failures, err)
			if _, err := s.db.ExecContext(ctx, "UPDATE meeting_blob_cleanup SET attempts=attempts+1 WHERE blob_key=?", key); err != nil {
				return errors.Join(failures, err)
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM meeting_blob_cleanup WHERE blob_key=?", key); err != nil {
			return err
		}
	}
	return failures
}
