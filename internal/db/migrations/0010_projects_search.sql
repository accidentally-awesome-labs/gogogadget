-- +goose Up
ALTER TABLE projects ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', name)) STORED;
CREATE INDEX projects_search_idx ON projects USING GIN (search_tsv);

-- +goose Down
DROP INDEX IF EXISTS projects_search_idx;
ALTER TABLE projects DROP COLUMN IF EXISTS search_tsv;
