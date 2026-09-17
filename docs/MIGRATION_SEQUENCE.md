# Pending migration deployment sequence

These draft changes are cumulative, not independently deployable:

| Order | Draft / original PR | Migration | Required predecessor |
| --- | --- | --- | --- |
| 1 | #194 / #183 | 0024 file lexical lane | released/main schema 23 |
| 2 | #197 / #180 | 0025 HNSW indexes | #194, schema 24 |
| 3 | #195 / #185 | 0026 data-plane hygiene | #197, schema 25 |

The PR base chain is `main` → `codex/fix-pr-183` → `codex/fix-pr-180`
→ `codex/fix-pr-185`. Successor branches must include their predecessor schema and source. Local
repair branches are rebuilt on current main and replay the original authored
changes; published commit identities remain in the original PR history.
Keep this order when retargeting after a predecessor merges.

On 2026-09-10, main `2986fe38175f54d99f15dd38a498708c6ecd88cd` and published
tags `v0.1.0` / `v0.1.1` contain only migrations 0001–0023. This does not prove
that a private deployment never applied a draft. Consequently migration
numbers and SQL identities are retained, not renumbered on an assumption.

HNSW DDL and shipping text-query planner acceptance are separate evidence:
creating indexes does not prove that a text query uses them. An index-only
successor must describe that partial scope and leave the broader #173 acceptance
open. #195 still requires the predecessor schema 25 regardless of query strategy.
This document does not approve a product decision or query-strategy change,
waive a review gate, or authorize deployment. #176's model-free file-lane RFC
also requires a maintainer decision before this draft is made review-ready.

Goose startup remains strict: no `WithAllowMissing` or equivalent option is
enabled. A database that already applied 26 while omitting 24/25 will correctly
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
to the branch's declared head (24, 25, or 26). Each step checks full applied
history and preserved data; subsequent steps check lexical backfill, valid
HNSW DDL, and deduplication/unique rejection. Finally the ordinary production
`DB.Migrate` startup path must accept the resulting history unchanged.

The dedicated test uses `MEM_MIGRATION_SEQUENCE_TEST_DB`, refuses a database
that already has Goose history, and must not target any developer or production
database. The existing owned-database runner performs cleanup. These are real
database tests over synthetic fixtures, not retrieval-quality or latency proof.
