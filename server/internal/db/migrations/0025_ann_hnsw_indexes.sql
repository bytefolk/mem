-- +goose Up
-- Transactional startup migration: CREATE INDEX blocks writes on these tables
-- until commit. Schedule a maintenance window for a populated deployment.
--
-- Operator class vector_cosine_ops matches the <=> operator used by search
-- and relator. pgvector defaults m=16, ef_construction=64; tune only after
-- representative recall/build/ingest measurements.
--
-- Wrong-dimension vectors fail at INSERT/UPDATE against the fixed
-- vector(768)/vector(512) columns, before index maintenance. NULL embeddings
-- are allowed by the table DDL and are not present in a cosine HNSW index.
-- This file does not use CREATE INDEX CONCURRENTLY: a failed concurrent build
-- leaves an INVALID index that IF NOT EXISTS will skip.
--
-- index_generation_vectors is intentionally not indexed (undimensioned; no
-- generation executor yet). Face clustering is still in-process; the face
-- index is DDL for a future SQL kNN, not a measured face-query speedup.
--
-- Refs: https://github.com/bytefolk/mem/issues/173

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_embeddings_text_embedding_hnsw
    ON embeddings_text USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_embeddings_visual_embedding_hnsw
    ON embeddings_visual USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_embeddings_face_embedding_hnsw
    ON embeddings_face USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_embeddings_face_embedding_hnsw;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_embeddings_visual_embedding_hnsw;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_embeddings_text_embedding_hnsw;
-- +goose StatementEnd
