-- +goose Up
-- +goose StatementBegin
-- HNSW ANN index for text embeddings (768-d, cosine distance).
-- Used by search route "text" (search.go) and relator same-topic (relator.go).
CREATE INDEX IF NOT EXISTS idx_embeddings_text_embedding_hnsw
    ON embeddings_text USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- HNSW ANN index for visual embeddings (512-d, cosine distance).
-- Used by search route "visual" (search.go) and relator same-event (relator.go).
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
