# Pending migration deployment sequence

These draft changes are cumulative, not independently deployable:

| Order | Draft / original PR | Migration | Required predecessor |
| --- | --- | --- | --- |
| 1 | #194 / #183 | 0024 file lexical lane | released/main schema 23 (merged) |
| 2 | #173 HNSW completion (supersedes #197 HOLD) | 0025 HNSW indexes + text continuation | #194, schema 24 |
| 3 | #195 / #185 | 0027 data-plane hygiene | schema 26 |

Migrations 0024–0026 are now on `main`. PR #195 must integrate that exact
history before adding data-plane hygiene as 0027. Local repair branches retain
the original authored commits and add a current-main integration commit rather
than rewriting published history.

The hygiene migration was originally reviewed as draft 0026, but main now owns
0026 for `memory_producer_agent`. The unmerged hygiene migration therefore
moves to 0027. An operator who privately applied the old draft under version 26
must stop and obtain a recovery plan; do not rewrite Goose history to make the
new main sequence appear valid.

Migration 0025 creates the three cosine HNSW indexes. The shipping text route
no longer uses `DISTINCT ON (f.id) ORDER BY f.id` as its primary plan: it walks
cosine-ordered candidates and falls back to that exact query only when a bounded
scan underfills. Visual cosine-order already matched HNSW. Face DDL is not a
face-query speedup. #195 still requires predecessor schema 25. Recall and live
latency remain `#175`, not this migration. #195 now requires predecessor schema
26.

This document does not waive a review gate or authorize deployment.

Goose startup remains strict: no `WithAllowMissing` or equivalent option is
enabled. A database that already applied 27 while omitting 24/25/26 will correctly
fail startup against the cumulative schema. Stop and obtain an operator-owned
recovery plan for such a database; do not edit its migration history, renumber
its SQL, or apply lower versions out of order to manufacture a pass.

## Migration 0024 operational boundary

Adding the stored generated `search_tsv` column rewrites existing `files` rows,
and its two indexes are built without `CONCURRENTLY`. Schedule a maintenance
window sized for the file corpus and expect table locks to block other access.
The migration indexes filenames only; `PathPrefix` remains a filter, not path
substring retrieval. Downgrading 0024 removes the derived column/indexes and
requires deploying server code that does not query the lexical route.

Goose runs this migration transactionally, so an ordinary failure rolls back
its DDL. If an operator has applied some statements manually, `IF NOT EXISTS`
does not prove that an existing column or index has the correct definition.
Inspect both `goose_db_version` and the actual schema/index definitions before
an operator-owned recovery; do not mark an unverified partial schema applied.

## Regression evidence

`TestMigrationFilesContiguous` rejects embedded numeric gaps without a DB.
`scripts/verify.sh integration` creates a separate, owned `_test` database and
runs `TestMigrationUpgradeSequence`. It applies real Goose migrations to 23,
seeds a file with duplicate text chunks, then advances one version at a time
to the branch's declared head (24 through 27). Each step checks full applied
history and preserved data; subsequent steps check lexical backfill, valid
HNSW DDL, producer-agent indexing, and hygiene deduplication/unique rejection.
Finally the ordinary production
`DB.Migrate` startup path must accept the resulting history unchanged.

The dedicated test uses `MEM_MIGRATION_SEQUENCE_TEST_DB`, refuses a database
that already has Goose history, and must not target any developer or production
database. The existing owned-database runner performs cleanup. These are real
database tests over synthetic fixtures, not retrieval-quality or latency proof.
