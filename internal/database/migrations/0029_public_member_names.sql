-- +goose Up
ALTER TABLE people ADD COLUMN public_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE people DROP COLUMN public_name;
