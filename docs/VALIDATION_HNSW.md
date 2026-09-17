# HNSW index foundation — partial delivery for #173

Migration `0025_ann_hnsw_indexes.sql` adds cosine HNSW indexes to text (768),
visual (512), and face (512) embeddings. Migration 0024 belongs to #183 and
0026 to #185. The integration runner declares this branch's head as 25.

## Scope and acceptance

This is the index foundation portion of #173, tracked with **Refs #173**.
It does not complete that issue. The shipping text route remains exact and is
not rewritten: a bounded ANN candidate set cannot establish best-chunk-per-file
results merely by returning k distinct files. Underfill fallback alone also
does not establish that unseen files or better chunks cannot outrank them.
A retrieval-policy change needs its own continuation/exhaustion/fallback and
quality acceptance. The strict planner gate below is retained unchanged in
meaning, and still fails for text.

The independently testable criteria for this partial change are:

- a populated 24 → 25 → 24 → 25 migration preserves text/visual/face vectors;
- all three valid indexes use HNSW and cosine operators;
- ingest and ordinary startup work after the migration; wrong dimensions fail;
- shipping text search still returns k distinct eligible files with the best
  chunk even when one file holds 101 nearest chunks; owner, literal path,
  allow-list, MIME and time boundaries remain enforced;
- the separate full-issue planner check cannot report success when text misses
  HNSW. Face DDL is never reported as a shipping face search improvement.

This branch requires #194's lexical migration 0024; the
[cumulative migration sequence](MIGRATION_SEQUENCE.md) is included by that base.
Deployment order stays #194 → #197 → #195. Accepting this partial scope is a
review decision, not a claim that the remaining #173 planner criterion passed.

## Reproducible local gates

```bash
# Creates separate owned test databases, including a populated HNSW round trip,
# and runs the shipping text semantics regression. MEM_TEST_DB ends in _test.
MEM_TEST_DB="$MEM_TEST_DB" ./scripts/verify.sh integration
MEM_TEST_DB="$MEM_TEST_DB" ./scripts/verify.sh integration-race
```

`TestHNSWMigrationPostgres` requires a fresh database via `MEM_HNSW_TEST_DB`
and refuses existing Goose history. It seeds 2,000 vectors in each table before
index construction, checks rollback preservation, repeats the upgrade, writes
one more vector in each table, rejects wrong dimensions, and calls `DB.Migrate`.
`TestTextANNFileSemanticsPostgres` exercises the actual `runTextANN` method;
it is a result-semantics regression, not a planner-use assertion.

## Full #173 planner gate (still unmet)

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
criterion, not a regression introduced by the index DDL. The full-issue verification
must remain failed until that criterion is met. The partial index scope above
does not weaken that check. This is still an unmet feature acceptance criterion even
though the query predates this PR; an unchanged baseline is not a waiver.

### Historical bounded rewrite investigation (2026-09-10)

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

## Operational limits

Migration 0025 remains transactional, consistent with startup migrations.
Regular `CREATE INDEX` blocks writes on the indexed tables until commit; plan
an ingestion maintenance window for a populated deployment. The rollback test
proves data preservation, not uninterrupted concurrent ingest or zero downtime.
`CREATE INDEX CONCURRENTLY` would need a separate nontransactional recovery
policy, including invalid/partially built indexes, and is not silently adopted.

The indexes retain pgvector defaults `m=16`, `ef_construction=64`. Follow
[pgvector's index/scan documentation](https://github.com/pgvector/pgvector#hnsw)
and measure representative recall, build time and ingest cost before tuning;
there is no asserted safe row-count threshold. Iterative scans still have
scan/memory limits and do not establish exact file-level result equivalence.
NULL and zero vectors are not indexed for cosine distance.

The strict verification script stops immediately on SQL/connection errors.
Its pass/fail counters summarize completed assertions, not infrastructure
errors; an interrupted run is never a pass. No `|| true` masks SQL failures.

The former MinIO lifecycle pull failure is fixed by the inherited main commit
`f1cc9eb` (#209), which uses pinned `quay.io/minio` images. No extra image or
workflow override belongs to this HNSW diff.
