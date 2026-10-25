-- +goose Up
ALTER TABLE web_sessions ADD COLUMN idle_expires_at TEXT;
ALTER TABLE web_sessions ADD COLUMN remembered INTEGER NOT NULL DEFAULT 0 CHECK (remembered IN (0,1));
ALTER TABLE web_sessions ADD COLUMN login_role TEXT NOT NULL DEFAULT '';
UPDATE web_sessions SET idle_expires_at=min(expires_at,strftime('%Y-%m-%dT%H:%M:%SZ','now','+30 minutes')),
    login_role=coalesce((SELECT role FROM users WHERE users.id=web_sessions.user_id),'')
    WHERE user_id IS NOT NULL;

-- +goose Down
ALTER TABLE web_sessions DROP COLUMN login_role;
ALTER TABLE web_sessions DROP COLUMN remembered;
ALTER TABLE web_sessions DROP COLUMN idle_expires_at;
