-- +goose Up
ALTER TABLE users ADD COLUMN public_email TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN public_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN invitation_pending INTEGER NOT NULL DEFAULT 0 CHECK(invitation_pending IN (0,1));

-- +goose Down
ALTER TABLE users DROP COLUMN invitation_pending;
ALTER TABLE users DROP COLUMN public_email;
ALTER TABLE users DROP COLUMN public_name;
