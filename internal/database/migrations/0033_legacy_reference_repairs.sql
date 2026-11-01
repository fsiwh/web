-- +goose Up
-- Preserve the original data before repairing legacy references created while
-- foreign-key enforcement was absent. Never invent accounts for orphan mail or
-- assign an existing account to an unrelated study program.
CREATE TABLE legacy_reference_repairs (
    source_table TEXT NOT NULL CHECK(source_table IN ('users','notifications')),
    record_id INTEGER NOT NULL,
    original TEXT NOT NULL CHECK(json_valid(original)),
    recorded_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    PRIMARY KEY(source_table,record_id)
) STRICT;

-- +goose StatementBegin
CREATE TRIGGER trg_utc_legacy_reference_repairs_insert
BEFORE INSERT ON legacy_reference_repairs
WHEN NOT (typeof(NEW.recorded_at)='text' AND length(NEW.recorded_at)=20
    AND strftime('%Y-%m-%dT%H:%M:%SZ',NEW.recorded_at,'+0 seconds') IS NEW.recorded_at)
BEGIN
    SELECT RAISE(ABORT,'timestamps must be UTC RFC 3339 ending in Z');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER trg_utc_legacy_reference_repairs_update
BEFORE UPDATE ON legacy_reference_repairs
WHEN NOT (typeof(NEW.recorded_at)='text' AND length(NEW.recorded_at)=20
    AND strftime('%Y-%m-%dT%H:%M:%SZ',NEW.recorded_at,'+0 seconds') IS NEW.recorded_at)
BEGIN
    SELECT RAISE(ABORT,'timestamps must be UTC RFC 3339 ending in Z');
END;
-- +goose StatementEnd

INSERT INTO legacy_reference_repairs(source_table,record_id,original)
SELECT 'users',id,json_object('program_id',program_id)
FROM users WHERE program_id IS NOT NULL
AND NOT EXISTS(SELECT 1 FROM programs p WHERE p.id=users.program_id);

UPDATE users SET program_id=NULL WHERE program_id IS NOT NULL
AND NOT EXISTS(SELECT 1 FROM programs p WHERE p.id=users.program_id);

INSERT INTO legacy_reference_repairs(source_table,record_id,original)
SELECT 'notifications',id,json_object(
    'id',id,'user_id',user_id,'title',title,'message',message,'type',type,
    'link',link,'read',read,'created_at',created_at,'updated_at',updated_at,
    'deleted_at',deleted_at)
FROM notifications
WHERE NOT EXISTS(SELECT 1 FROM users u WHERE u.id=notifications.user_id);

DELETE FROM notifications
WHERE NOT EXISTS(SELECT 1 FROM users u WHERE u.id=notifications.user_id);

-- +goose Down
-- Restoring known invalid foreign keys would undermine runtime enforcement.
-- Keep the preserved originals available for explicit administrative recovery.
