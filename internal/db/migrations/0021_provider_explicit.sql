-- +goose Up
ALTER TABLE subscriptions ALTER COLUMN provider DROP DEFAULT;

-- +goose Down
ALTER TABLE subscriptions ALTER COLUMN provider SET DEFAULT 'polar';
