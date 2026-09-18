# HNSW index + text continuation for #173

Migration `0025_ann_hnsw_indexes.sql` adds cosine HNSW indexes to text (768),
visual (512), and face (512) embeddings. Main already shipped lexical
migration 0024; this branch's head is 25.

## What this change proves

- Populated 24 → 25 → 24 → 25 preserves text/visual/face vectors and rebuilds
  three `VALID` `vector_cosine_ops` HNSW indexes (`TestHNSWMigrationPostgres`).
- Post-index INSERT succeeds; an UPDATE to the wrong dimension is rejected by
  the `vector(N)` column type (failure mode: PostgreSQL dimension error, not a
  silent pad/truncate).
- `EXPLAIN (ANALYZE)` of the shipping text cosine-order query and the visual
  cosine-order query names `idx_embeddings_text_embedding_hnsw` and
  `idx_embeddings_visual_embedding_hnsw` on a 2,000-row corpus. Planner
  settings are not forced.
- `TestTextANNFileSemanticsPostgres` keeps best-chunk-per-file top-k when one
  file owns 101 nearest chunks, and still enforces owner, literal path,
  allow-list, MIME, and time filters. Invalid allow-lists fail closed.

## Text continuation / fallback

A bounded `ORDER BY distance LIMIT n` scan can underfill after per-file
deduplication (`ef_search=40` returning 40 chunks of one file). The shipping
path:

1. Run a CTE `ORDER BY embedding <=> $1 LIMIT remaining` on `embeddings_text`
   (HNSW-compatible; omit `ANY(exclude)` when the exclude list is empty).
2. Join those candidates to `files` and apply owner/path/MIME/time filters.
3. Keep the first sighting of each file (that chunk is the file's best).
4. Repeat, excluding selected files, until k files are collected.
5. If a round returns no new files, fill the remainder with the original
   exact `DISTINCT ON (f.id) ORDER BY f.id, distance` query.

Step 1 is the planner-usable shape. Step 4 preserves the previous result
contract on pathological corpora. Iterative-scan GUC is not enabled.

## What this change does not prove

- Live embedding quality, production latency, index build time, or numerical
  recall. Those belong to [#175](https://github.com/bytefolk/mem/issues/175)
  (shipping search-path producer) and the closed producer attempt
  [#184](https://github.com/bytefolk/mem/pull/184). Fixture scores are not
  substituted.
- Face query speedup. `assignCluster` still averages centroids in Go.
- `index_generation_vectors` ANN. The column is undimensioned.

## Local gates

```bash
MEM_TEST_DB="$MEM_TEST_DB" ./scripts/verify.sh integration
```

`run_hnsw_migration` creates a fresh `_test` database, runs
`TestHNSWMigrationPostgres`, then `scripts/verify_hnsw_indexes.sh` when `psql`
is available.

Face evidence is valid DDL and populated-table migration, not a measured
face-query speedup.
