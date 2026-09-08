-- +goose Up
-- Model-free lexical lane for the file corpus.  Mirrors the FTS + trigram
-- shape already established for memories (0008) so that filename and path
-- substring search works without an embedding worker.

-- +goose StatementBegin
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS search_tsv tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(name, ''))
    ) STORED;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_files_search_tsv
    ON files USING gin (search_tsv);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_files_name_trgm
    ON files USING gin (lower(name) gin_trgm_ops);
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS idx_files_name_trgm;
DROP INDEX IF EXISTS idx_files_search_tsv;
ALTER TABLE files DROP COLUMN IF EXISTS search_tsv;
