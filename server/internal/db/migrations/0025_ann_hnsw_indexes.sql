-- +goose Up
-- Transactional startup migration: index construction blocks writes on these
-- tables until commit. Schedule a maintenance window for populated deployments.
-- Keep pgvector defaults m=16, ef_construction=64; tune only after representative
-- recall/build/ingest measurements. No corpus-size or latency guarantee is made.
-- +goose StatementBegin
-- HNSW ANN index for text embeddings (768-d, cosine distance).
-- Available to direct cosine-order queries. Shipping text search deduplicates
-- by file before top-k and does NOT yet use this index; see VALIDATION_HNSW.md.
CREATE INDEX IF NOT EXISTS idx_embeddings_text_embedding_hnsw
    ON embeddings_text USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- HNSW ANN index for visual embeddings (512-d, cosine distance).
-- Compatible with the visual route distance ordering; planner use depends on
-- corpus and filters. This does not establish recall or production latency.
CREATE INDEX IF NOT EXISTS idx_embeddings_visual_embedding_hnsw
    ON embeddings_visual USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- HNSW ANN index for face embeddings (512-d, cosine distance).
-- Reserved for future face search queries; current clustering is O(n) in Go.
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
