package exams

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegulationImportPreservesAndVerifiesSnapshots(t *testing.T) {
	s, db := examsService(t)
	ctx := context.Background()
	directory := t.TempDir()
	for _, source := range regulationSources() {
		documents := append([]RegulationDocument{source.PO, source.Handbook}, source.Amendments...)
		documents = append(documents, source.Additional...)
		for _, doc := range documents {
			if err := os.WriteFile(filepath.Join(directory, filepath.Base(doc.URL)), []byte("%PDF-test"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.ImportRegulationDocuments(ctx, directory); err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow("SELECT count(*) FROM regulation_documents").Scan(&count)
	if count < 16 {
		t.Fatal("documents missing", count)
	}
	if err := s.ImportRegulationDocuments(ctx, directory); err != nil {
		t.Fatal("repeat import", err)
	}
	var again int
	db.QueryRow("SELECT count(*) FROM regulation_documents").Scan(&again)
	if again != count {
		t.Fatal("duplicate snapshots")
	}
	directoryResult, err := s.Regulations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := directoryResult.Filter(RegulationFilter{Version: "2023"}, 1)
	for _, record := range page.Records {
		if !strings.HasPrefix(record.Source.PO.URL, "/admin/regulation-documents/") {
			t.Fatal("not using stored PDF")
		}
	}
	var id, key string
	db.QueryRow("SELECT id,blob_key FROM regulation_documents LIMIT 1").Scan(&id, &key)
	object, _, err := s.RegulationDocument(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(data) != "%PDF-test" {
		t.Fatal("download corrupted")
	}
	if err := s.storage.(writableStorage).Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportRegulationDocuments(ctx, directory); err == nil {
		t.Fatal("missing stored object accepted")
	}
}
