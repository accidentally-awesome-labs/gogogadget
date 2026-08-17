-- +goose Up
ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS locale;
