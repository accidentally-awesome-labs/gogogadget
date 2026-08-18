-- +goose Up
CREATE TABLE announcements (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind       TEXT NOT NULL CHECK (kind IN ('info','warning','critical')),
  message    TEXT NOT NULL,
  url        TEXT NOT NULL DEFAULT '',
  active     BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- At most one banner is live at a time; enforced by the partial unique index.
CREATE UNIQUE INDEX announcements_one_active ON announcements (active) WHERE active;

-- +goose Down
DROP TABLE IF EXISTS announcements;
