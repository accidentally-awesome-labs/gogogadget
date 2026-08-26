-- +goose Up
-- The example workflow's table. This payload is what proves the retained
-- migration rule: removing workflow/example-ping deletes every authored file it
-- owns, but the migration the project already allocated a global number for
-- stays on disk and stays in the lock's ledger, because a database that has run
-- it cannot be told to un-run it.
CREATE TABLE example_ping_events (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  note       TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
