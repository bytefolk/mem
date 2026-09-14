-- +goose Up
-- +goose StatementBegin
-- HNSW ANN index for text embeddings (768-d, cosine distance).
-- Not proven used for either candidate consumer. scripts/verify_hnsw_indexes.sh
-- reports the text route's per-file DISTINCT ON shape (runTextANN,
-- server/internal/search/search.go) as a retained FAIL on a sequential scan, and
-- the relator same-topic query (recomputeText, server/internal/relator/relator.go)
-- leads its ORDER BY with e.file_id, so a HNSW KNN scan is structurally
-- unavailable for it. See docs/VALIDATION_HNSW.md for the provenance of that run.
CREATE INDEX IF NOT EXISTS idx_embeddings_text_embedding_hnsw
    ON embeddings_text USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- HNSW ANN index for visual embeddings (512-d, cosine distance).
-- The only usage evidence is an UNFILTERED probe, on a run recorded as
-- author-reported (see docs/VALIDATION_HNSW.md). scripts/verify_hnsw_indexes.sh
-- reproduces runVisualANN's distance-leading ORDER BY plus LIMIT
-- (server/internal/search/search.go) without the path/MIME/time predicates that
-- appendPathFilters and appendMIMEFilter add, so the filtered plan is not
-- established. The relator same-event query (recomputeVisual,
-- server/internal/relator/relator.go) is distance-leading yet unprobed.
CREATE INDEX IF NOT EXISTS idx_embeddings_visual_embedding_hnsw
    ON embeddings_visual USING hnsw (embedding vector_cosine_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- HNSW ANN index for face embeddings (512-d, cosine distance).
-- No consumer: there is no shipping SQL face search route and face clustering
-- stays in Go (server/internal/face/face.go), so this index is not exercised by
-- scripts/verify_hnsw_indexes.sh and its benefit is unmeasured. It costs write
-- amplification on every detected face.
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
