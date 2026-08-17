-- +goose Up
CREATE TABLE files (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  uploader_user_id TEXT NOT NULL,
  filename TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
  size_bytes BIGINT NOT NULL,
  storage_key TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX files_org_idx ON files (clerk_org_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS files;
