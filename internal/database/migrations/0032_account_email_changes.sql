-- +goose Up
-- Bind a replacement address to a specific, single-use verification link.
-- Consuming, expiring or replacing the token also removes the pending change.
CREATE TABLE account_email_changes (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_id TEXT NOT NULL UNIQUE REFERENCES user_tokens(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    previous_email TEXT NOT NULL
) STRICT;

-- Security notifications survive token consumption and SMTP retries.
CREATE TABLE account_change_mail (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('password_changed','email_changed')),
    recipient TEXT NOT NULL,
    name TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE account_change_mail;
DROP TABLE account_email_changes;
