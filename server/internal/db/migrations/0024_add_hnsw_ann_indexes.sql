-- +goose NO TRANSACTION
-- +goose Up
-- Add HNSW ANN indexes for all embedding tables so vector queries use
-- approximate nearest-neighbor search instead of exact sequential scans.
-- All three tables have fixed-dimension columns (768 for text, 512 for
-- visual and face), and all queries use cosine distance (<=>).
--
-- pgvector HNSW defaults: m=16, ef_construction=64. These are suitable
-- for the personal-corpus scale this server targets. Operators class
-- vector_cosine_ops matches the <=> distance operator used by search
-- and relator queries.
--
-- CONCURRENTLY requires running outside a transaction block, hence the
-- NO TRANSACTION annotation above.
--
-- Resolves: https://github.com/bytefolk/mem/issues/173

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_embeddings_text_embedding_hnsw
    ON embeddings_text USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_embeddings_visual_embedding_hnsw
    ON embeddings_visual USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_embeddings_face_embedding_hnsw
    ON embeddings_face USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_embeddings_face_embedding_hnsw;
DROP INDEX IF EXISTS idx_embeddings_visual_embedding_hnsw;
DROP INDEX IF EXISTS idx_embeddings_text_embedding_hnsw;
-- +goose StatementEnd
