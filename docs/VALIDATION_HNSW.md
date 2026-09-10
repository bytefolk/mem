# HNSW verification for #173 / #180

Migration `0025_ann_hnsw_indexes.sql` adds cosine HNSW indexes to text (768),
visual (512), and face (512) embeddings. Migration 0024 belongs to #183 and
0026 to #185. The integration runner declares this branch's head as 25.

Use PostgreSQL 16+ with pgvector, all migrations applied, and a populated
synthetic corpus in a disposable database whose name ends in `_test`:

```bash
bash scripts/verify_hnsw_indexes.sh "$MEM_TEST_DB" "$CORPUS_USER_UUID"
```

The read-only script validates the exact indexes and non-null vector counts
for the supplied corpus owner, and runs EXPLAIN ANALYZE for the shipping text
and visual ordering/deduplication shapes. It checks each route's index name,
propagates SQL errors, and exits nonzero if either route misses HNSW.
It does not disable sequential scans or claim a production latency threshold.

## Remaining acceptance boundary

The shipping text query already uses `DISTINCT ON (f.id)` ordered by file ID
before global top-k selection. A simplified `ORDER BY distance LIMIT` query
does not prove that this production query uses HNSW. This query shape is
unchanged from main: a failed text planner gate is an unmet optimization
criterion, not a regression introduced by the index DDL. The verification
must remain failed until that criterion is met or the issue owner explicitly
revises the scope. Changing retrieval ranking is outside this correction.

Face indexing has no shipping SQL search route; its evidence is valid DDL and
populated-table migration, not a measured face-query speedup.

Recall, real embedding quality, production latency, index build time and index
size are NOT VERIFIED. #175 / #184 track live retrieval benchmark evidence;
fixture tests are not live quality results. No numerical improvement or recall
percentage is asserted here.

Fixed `vector(768)` / `vector(512)` column types reject wrong dimensions on
insert, before HNSW construction. NULL vectors can remain in the tables but
are not indexed. Down removes only the three new indexes and retains data.
