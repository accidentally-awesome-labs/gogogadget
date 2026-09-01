-- +goose Up
-- Search documents are a generic Postgres FTS integration table. Payloads are
-- coalesced in the outbox so one transaction can replace a pending operation.
CREATE TABLE search_documents (
  tenant_id text NOT NULL,
  collection text NOT NULL,
  document_id text NOT NULL,
  text text NOT NULL DEFAULT '',
  fields jsonb NOT NULL DEFAULT '{}'::jsonb,
  search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', text)) STORED,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, collection, document_id)
);
CREATE INDEX search_documents_vector_idx ON search_documents USING GIN (search_vector);
CREATE TABLE search_outbox (
  tenant_id text NOT NULL,
  collection text NOT NULL,
  document_id text NOT NULL,
  operation text NOT NULL CHECK (operation IN ('upsert','delete')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, collection, document_id)
);
CREATE INDEX search_outbox_available_idx ON search_outbox (available_at, created_at);

-- +goose Down
DROP TABLE IF EXISTS search_outbox;
DROP TABLE IF EXISTS search_documents;
