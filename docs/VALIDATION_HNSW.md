# HNSW verification for #173 / #180

Migration `0025_ann_hnsw_indexes.sql` adds cosine HNSW indexes to text (768),
visual (512), and face (512) embeddings. Migration 0024 belongs to #183 and
0026 to #185. The integration runner declares this branch's head as 25.

This branch includes #194's lexical migration 0024 and source commits.
Deployment must follow the [cumulative migration sequence](MIGRATION_SEQUENCE.md):
#194 → #197 → #195. #195 remains blocked behind this PR's unmet query acceptance.

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
revises the scope. This is still an unmet feature acceptance criterion even
though the query predates this PR; an unchanged baseline is not a waiver.

### Bounded semantics-preserving rewrite investigation

The smallest tested direct-distance rewrite filtered each chunk with a
`NOT EXISTS` peer having a smaller distance (UUID tie-break), then ordered by
`e.embedding <=> query_vector LIMIT 10`. PostgreSQL 17.10 / pgvector 0.8.3
selected `idx_embeddings_text_embedding_hnsw`, with the existing file-ID
index serving the peer lookup, on the 2,000-file synthetic fixture. Planner
selection alone did not establish equivalent results.

A rollback-only 768-dimensional counterexample separated one file's 101 near
chunks from the other 1,999 files: the first near vector was `[1,0,0,...]`,
the next 100 were `[1,i*0.001,0,...]`, and other files used `[0,1,0,...]`.
At unmodified defaults (`hnsw.ef_search=40`, `hnsw.iterative_scan=off`), the
shipping exact per-file query returned 10 files; the indexed anti-join returned
only 1. EXPLAIN ANALYZE showed the HNSW scan returning 40 candidate chunks,
39 then eliminated by per-file deduplication. All fixture mutations rolled
back. This is synthetic semantic/planner evidence, not latency evidence.

A separate exact-distance counterexample rules out a fixed oversampling cap:
81 closest chunks belonging to one file consume `8*k=80` candidates for
`k=10`, leaving one file after deduplication when ten files exist. Increasing
a fixed multiplier cannot guarantee k distinct files for unbounded chunk counts.

Concrete design blocker: the shipping contract chooses the best chunk per
eligible file before global top-k. A bounded approximate candidate scan can
underfill after deduplication or filtering. A safe rewrite needs a tested
candidate-exhaustion/continuation and fallback policy, including per-file
best-chunk selection and existing authorization/path/MIME/time filters.
Enabling iterative scans alone still needs an explicit scan-limit/exhaustion
policy; it is not proof of equivalence. That policy is not implemented or
accepted here. The promising anti-join is therefore not shipped, the original
query remains unchanged, and the text planner check must continue failing.
No planner settings or acceptance criteria were weakened.

Face indexing has no shipping SQL search route; its evidence is valid DDL and
populated-table migration, not a measured face-query speedup.

Recall, real embedding quality, production latency, index build time and index
size are NOT VERIFIED. #175 / #184 track live retrieval benchmark evidence;
fixture tests are not live quality results. No numerical improvement or recall
percentage is asserted here.

Fixed `vector(768)` / `vector(512)` column types reject wrong dimensions on
insert, before HNSW construction. NULL vectors can remain in the tables but
are not indexed. Down removes only the three new indexes and retains data.
