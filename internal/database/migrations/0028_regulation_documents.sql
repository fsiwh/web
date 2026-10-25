-- +goose Up
CREATE TABLE regulation_documents (
    id TEXT PRIMARY KEY,
    source_url TEXT NOT NULL UNIQUE,
    filename TEXT NOT NULL,
    blob_key TEXT NOT NULL UNIQUE,
    sha256 TEXT NOT NULL,
    nbytes INTEGER NOT NULL CHECK(nbytes>0),
    imported_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
-- +goose StatementBegin
CREATE TRIGGER trg_utc_regulation_documents_insert BEFORE INSERT ON regulation_documents
WHEN NOT (typeof(NEW.imported_at)='text' AND length(NEW.imported_at)=20 AND strftime('%Y-%m-%dT%H:%M:%SZ',NEW.imported_at,'+0 seconds') IS NEW.imported_at)
BEGIN SELECT RAISE(ABORT,'non-canonical UTC timestamp'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER trg_utc_regulation_documents_update BEFORE UPDATE ON regulation_documents
WHEN NOT (typeof(NEW.imported_at)='text' AND length(NEW.imported_at)=20 AND strftime('%Y-%m-%dT%H:%M:%SZ',NEW.imported_at,'+0 seconds') IS NEW.imported_at)
BEGIN SELECT RAISE(ABORT,'non-canonical UTC timestamp'); END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE regulation_documents;
