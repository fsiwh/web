package exams

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/fachschaftinformatik/web/internal/publicid"
	"github.com/fachschaftinformatik/web/model/meetings"
	"github.com/fachschaftinformatik/web/storage"
)

// ImportRegulationDocuments installs reviewed downloads into the configured
// object store. Existing snapshots are verified and preserved on repeat imports.
func (s *Service) ImportRegulationDocuments(ctx context.Context, directory string) error {
	provider, ok := s.storage.(writableStorage)
	if !ok {
		return ErrStorage
	}
	documents := map[string]bool{}
	for _, source := range regulationSources() {
		all := append([]RegulationDocument{source.PO, source.Handbook}, source.Amendments...)
		all = append(all, source.Additional...)
		for _, d := range all {
			documents[d.URL] = true
		}
	}
	for source := range documents {
		u, err := url.Parse(source)
		if err != nil {
			return err
		}
		filename := filepath.Base(u.Path)
		data, err := os.ReadFile(filepath.Join(directory, filename))
		if err != nil {
			return err
		}
		if len(data) > meetings.MaxPDFBytes || !bytes.HasPrefix(data, []byte("%PDF-")) {
			return fmt.Errorf("invalid PDF: %s", filename)
		}
		digest := sha256.Sum256(data)
		checksum := hex.EncodeToString(digest[:])
		var key, storedChecksum string
		var size int64
		err = s.db.QueryRowContext(ctx, "SELECT blob_key,sha256,nbytes FROM regulation_documents WHERE source_url=?", source).Scan(&key, &storedChecksum, &size)
		if err == nil {
			if storedChecksum != checksum {
				return fmt.Errorf("snapshot differs: %s", filename)
			}
			if err := verifyStoredObject(ctx, s.storage, key, checksum, size); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		id := publicid.New()
		key = "regulations/" + id + "/" + filename
		if err = provider.Upload(ctx, key, bytes.NewReader(data), int64(len(data)), "application/pdf"); err != nil {
			_ = provider.Delete(ctx, key)
			return err
		}
		_, err = s.db.ExecContext(ctx, "INSERT INTO regulation_documents(id,source_url,filename,blob_key,sha256,nbytes) VALUES(?,?,?,?,?,?)", id, source, filename, key, checksum, len(data))
		if err != nil {
			return errors.Join(err, provider.Delete(ctx, key))
		}
	}
	return nil
}

func (s *Service) RegulationDocument(ctx context.Context, id string) (*storage.Object, string, error) {
	var key, filename string
	if err := s.db.QueryRowContext(ctx, "SELECT blob_key,filename FROM regulation_documents WHERE id=?", id).Scan(&key, &filename); err != nil {
		return nil, "", err
	}
	object, err := s.storage.GetObject(ctx, key)
	return object, filename, err
}

func (s *Service) storedRegulationSources(ctx context.Context) ([]RegulationSource, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT source_url,id FROM regulation_documents")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := map[string]string{}
	for rows.Next() {
		var source, id string
		if err := rows.Scan(&source, &id); err != nil {
			return nil, err
		}
		links[source] = "/admin/regulation-documents/" + id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sources := regulationSources()
	replace := func(d *RegulationDocument) {
		if link := links[d.URL]; link != "" {
			d.URL = link
		}
	}
	for i := range sources {
		source := &sources[i]
		replace(&source.PO)
		replace(&source.Handbook)
		for j := range source.Amendments {
			replace(&source.Amendments[j])
		}
		for j := range source.Additional {
			replace(&source.Additional[j])
		}
	}
	return sources, nil
}
