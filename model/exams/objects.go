package exams

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
	"time"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/model/meetings"
	objectstore "github.com/fachschaftinformatik/web/storage"
)

const storageOperationTimeout = 30 * time.Second

type stagedObject struct {
	checksum, key string
	size          int64
	existing      bool
}

// Verify or upload immutable bytes before acquiring SQLite's writer lock. Only
// the final short transaction may attach the staged object to an archive row.
func stageObject(ctx context.Context, db database.DBTX, storage writableStorage, value []byte, attempts *[]string) (stagedObject, error) {
	digest := sha256.Sum256(value)
	checksum := hex.EncodeToString(digest[:])
	var key string
	var size int64
	err := db.QueryRowContext(ctx, "SELECT blob_key,nbytes FROM archive_objects WHERE checksum=?", checksum).Scan(&key, &size)
	if err == nil {
		object, err := storage.GetObject(ctx, key)
		if err != nil {
			return stagedObject{}, fmt.Errorf("%w: %w", ErrStorage, err)
		}
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(object, meetings.MaxPDFBytes+1))
		readErr = errors.Join(readErr, object.Close())
		if readErr != nil {
			return stagedObject{}, fmt.Errorf("%w: %w", ErrStorage, readErr)
		}
		if n != int64(len(value)) || size != n || hex.EncodeToString(hash.Sum(nil)) != checksum {
			return stagedObject{}, fmt.Errorf("%w: Prüfsumme der vorhandenen Datei stimmt nicht überein", ErrStorage)
		}
		return stagedObject{checksum: checksum, key: key, size: size, existing: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return stagedObject{}, err
	}
	// A failed transaction must only remove its own upload, even if another
	// process concurrently retries the same checksum after the rollback.
	key = "archive/objects/" + checksum + "/" + rand.Text() + ".pdf"
	*attempts = append(*attempts, key)
	if err := storage.Upload(ctx, key, bytes.NewReader(value), int64(len(value)), "application/pdf"); err != nil {
		return stagedObject{}, fmt.Errorf("%w: %w", ErrStorage, err)
	}
	return stagedObject{checksum: checksum, key: key, size: int64(len(value))}, nil
}

func (o stagedObject) attach(ctx context.Context, tx *sql.Tx) (string, error) {
	if o.existing {
		var key string
		var size int64
		if err := tx.QueryRowContext(ctx, "SELECT blob_key,nbytes FROM archive_objects WHERE checksum=?", o.checksum).Scan(&key, &size); errors.Is(err, sql.ErrNoRows) {
			return "", ErrConflict
		} else if err != nil {
			return "", err
		}
		// A retired object must never be resurrected after cleanup selected it.
		if key != o.key || size != o.size {
			return "", ErrConflict
		}
	} else {
		result, err := tx.ExecContext(ctx, "INSERT INTO archive_objects(checksum,blob_key,nbytes) VALUES(?,?,?) ON CONFLICT(checksum) DO NOTHING", o.checksum, o.key, o.size)
		if err != nil {
			return "", err
		}
		if n, err := result.RowsAffected(); err != nil {
			return "", err
		} else if n != 1 {
			return "", ErrConflict
		}
	}
	return o.checksum, nil
}

// CleanupBlobs removes unreferenced draft objects and compacts verified legacy
// copies in bounded batches. Shared targets and old download URLs stay intact.
func (s *Service) CleanupBlobs(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, storageOperationTimeout)
	defer cancel()
	storage, ok := s.storage.(writableStorage)
	if !ok {
		return nil
	}
	// Objects are immutable and an existing staged key must still match at
	// attachment. Retired keys cannot be resurrected, so deletion needs no lock.
	rows, err := s.db.QueryContext(ctx, "SELECT blob_key FROM archive_blob_cleanup WHERE NOT EXISTS(SELECT 1 FROM archive_objects o WHERE o.blob_key=archive_blob_cleanup.blob_key) LIMIT 16")
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
		if err := storage.Delete(ctx, key); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM archive_blob_cleanup WHERE blob_key=?", key); err != nil {
			return err
		}
	}
	rows, err = s.db.QueryContext(ctx, `SELECT c.blob_key,c.checksum,o.blob_key,o.nbytes FROM archive_legacy_copies c JOIN archive_objects o ON o.checksum=c.checksum ORDER BY c.blob_key LIMIT 16`)
	if err != nil {
		return err
	}
	type redundant struct {
		key, checksum, target string
		size                  int64
	}
	var copies []redundant
	for rows.Next() {
		var copy redundant
		if err := rows.Scan(&copy.key, &copy.checksum, &copy.target, &copy.size); err != nil {
			rows.Close()
			return err
		}
		copies = append(copies, copy)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	for _, copy := range copies {
		// Verify both byte streams before discarding a legacy copy. The shared
		// target is immutable and remains referenced by every old download URL.
		if err := verifyStoredObject(ctx, storage, copy.target, copy.checksum, copy.size); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if err := verifyStoredObject(ctx, storage, copy.key, copy.checksum, copy.size); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
			failures = errors.Join(failures, err)
			continue
		}
		// A concurrent edit may retire this target while it was being verified.
		// Keep the legacy source until a later pass can verify the new target.
		var active bool
		if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM archive_objects WHERE checksum=? AND blob_key=?)", copy.checksum, copy.target).Scan(&active); err != nil {
			return err
		}
		if !active {
			continue
		}
		if err := storage.Delete(ctx, copy.key); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM archive_legacy_copies WHERE blob_key=?", copy.key); err != nil {
			return err
		}
	}
	return failures
}

func verifyStoredObject(ctx context.Context, storage Storage, key, checksum string, size int64) error {
	object, err := storage.GetObject(ctx, key)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(object, size+1))
	err = errors.Join(err, object.Close())
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != checksum {
		return fmt.Errorf("%w: checksum or size mismatch for %s", ErrStorage, key)
	}
	return nil
}
