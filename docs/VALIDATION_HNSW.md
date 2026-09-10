# HNSW ANN Index Validation Ledger

**Issue:** #173  
**PR:** #180  
**Migration:** 0025_ann_hnsw_indexes.sql  
**Date:** 2026-09-08  
**Evidence Level:** E2 → E3 (pending database verification)

## Summary

This document records the validation of HNSW ANN indexes added to the three embedding tables to resolve issue #173 (vector queries performing sequential scans).

## Changes

Migration 0025 adds three HNSW indexes using pgvector's `vector_cosine_ops`:

1. `idx_embeddings_text_embedding_hnsw` on `embeddings_text.embedding` (768-d)
2. `idx_embeddings_visual_embedding_hnsw` on `embeddings_visual.embedding` (512-d)
3. `idx_embeddings_face_embedding_hnsw` on `embeddings_face.embedding` (512-d)

## Test Methodology

### Prerequisites
- PostgreSQL 16+ with pgvector extension (shipped in `pgvector/pgvector:pg16` image)
- All migrations applied (0001 through 0025)
- Populated test corpus with all three embedding dimensions

### Test Queries

**Text search (search.go:666-674):**
```sql
SELECT e.id, e.file_id, e.chunk_index,
       1 - (e.embedding <=> $1::vector) AS score,
       e.chunk_text AS snippet
  FROM embeddings_text e
  JOIN files f ON f.id = e.file_id
 WHERE f.user_id = $2
 ORDER BY e.embedding <=> $1::vector ASC
 LIMIT $3;
```

**Visual search (search.go:705-711):**
```sql
SELECT e.file_id,
       (1 - (e.embedding <=> $1::vector))::real AS score
  FROM embeddings_visual e
  JOIN files f ON f.id = e.file_id
 WHERE f.user_id = $2
 ORDER BY e.embedding <=> $1::vector ASC
 LIMIT $3;
```

### Expected Results

**Before migration 0025:**
```
Sort  (cost=12345.67..12345.70 rows=10 width=100)
  Sort Key: (e.embedding <=> $1) ASC
  Sort Method: top-N heapsort  Memory: 25kB
  ->  Nested Loop  (cost=0.00..12345.47 rows=10 width=100)
        ->  Seq Scan on embeddings_text e  (cost=0.00..12000.00 rows=1000 width=80)
              Filter: (embedding IS NOT NULL)
        ->  Index Scan using files_pkey on files f  (cost=0.42..0.34 rows=1 width=20)
              Index Cond: (id = e.file_id)
              Filter: (user_id = $2)
```

**After migration 0025:**
```
Limit  (cost=12.34..12.36 rows=10 width=100)
  ->  Nested Loop  (cost=12.34..123.45 rows=10 width=100)
        ->  Index Scan using idx_embeddings_text_embedding_hnsw on embeddings_text e  (cost=12.34..120.00 rows=100 width=80)
              Order By: (embedding <=> $1)
        ->  Index Scan using files_pkey on files f  (cost=0.42..0.34 rows=1 width=20)
              Index Cond: (id = e.file_id)
              Filter: (user_id = $2)
```

Key differences:
- `Seq Scan on embeddings_text` → `Index Scan using idx_embeddings_text_embedding_hnsw`
- Cost reduction from ~12000 to ~120 (100x improvement for large tables)
- `Order By` pushed down to index (no separate Sort node)

## Verification Status

### ✅ Completed
- [x] Migration 0025 created with correct DDL
- [x] Indexes use `vector_cosine_ops` matching query operators (`<=>`)
- [x] Dimensions match schema (768 for text, 512 for visual/face)
- [x] Deferral comments updated in 0001_init.sql and 0019_versioned_index_generations.sql
- [x] SPEC.md:537 already correctly states HNSW contract (no change needed)
- [x] Verification script created: `scripts/verify_hnsw_indexes.sh`

### ⏳ Pending (requires database environment)
- [ ] Run `scripts/verify_hnsw_indexes.sh` against populated test database
- [ ] Confirm EXPLAIN shows index scans for text and visual queries
- [ ] Measure query latency before/after on realistic corpus (10k+ vectors)
- [ ] Verify recall is unchanged (HNSW is approximate, but with default parameters should be >95%)
- [ ] Test migration on table with inconsistent dimensions (should fail gracefully or succeed if all rows match schema)

### ℹ️ Notes
- HNSW build time: ~1-2 seconds per 10k rows (one-time cost during migration)
- Index size: ~1.5x raw vector size (768-d × 4 bytes × 1.5 ≈ 4.6KB per row)
- Default parameters: `m=16`, `ef_construction=64` (balanced for recall vs build time)
- For higher recall (>98%), consider `m=32`, `ef_construction=128` (slower build, larger index)

## Failure Modes

### Inconsistent dimensions
If `embeddings_text` contains rows with dimensions != 768, the index creation will fail:
```
ERROR:  expected 768 dimensions, got 512
```
This is correct behavior — the schema enforces `vector(768)`, so inconsistent data indicates a bug upstream.

### Empty table
HNSW index can be created on an empty table. pgvector will build a minimal index structure that accepts inserts. No error.

### NULL embeddings
Rows with `embedding IS NULL` are excluded from the index. Queries filtering on `embedding IS NOT NULL` will use the index for non-NULL rows.

## Recall Measurement

Recall was not measured in this validation. Recommended approach:

1. Generate 10k random query vectors (same distribution as production embeddings)
2. For each query, compute exact top-10 using sequential scan
3. For each query, compute approximate top-10 using HNSW index
4. Recall = |intersection| / 10, averaged over all queries
5. Expected recall with default parameters: 0.95-0.98

Alternative: Use pgvector's built-in benchmark tool if available, or reference the harness in issue #XXX (if one exists).

## Conclusion

The DDL changes are correct and match the query patterns. Actual performance verification requires a populated database environment. The verification script `scripts/verify_hnsw_indexes.sh` is provided for maintainers to run against their test infrastructure.

**Recommendation:** Merge after a maintainer runs the verification script against a test database and confirms index scans are used.
