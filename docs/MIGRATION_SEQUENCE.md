# Pending migration deployment sequence

These draft changes are cumulative, not independently deployable:

| Order | Draft / original PR | Migration | Required predecessor |
| --- | --- | --- | --- |
| 1 | #194 / #183 | 0024 file lexical lane | released/main schema 23 |
| 2 | #197 / #180 | 0025 HNSW indexes | #194, schema 24 |
| 3 | #195 / #185 | 0026 data-plane hygiene | #197, schema 25 |

The PR base chain is `main` → `codex/fix-pr-183` → `codex/fix-pr-180`
→ `codex/fix-pr-185`. Successor branches include their predecessor source
commits through forward merges; original authored commits are preserved.
Keep this order when retargeting after a predecessor merges.

On 2026-09-10, main `2986fe38175f54d99f15dd38a498708c6ecd88cd` and published
tags `v0.1.0` / `v0.1.1` contain only migrations 0001–0023. This does not prove
that a private deployment never applied a draft. Consequently migration
numbers and SQL identities are retained, not renumbered on an assumption.

**#197 remains HOLD for the shipping text-query planner acceptance. #195 is
HOLD behind #197 even if its own CI passes.** This document does not approve
a query-strategy change, waive a review gate, or authorize deployment.

Goose startup remains strict: no `WithAllowMissing` or equivalent option is
enabled. A database that already applied 26 while omitting 24/25 will correctly
fail startup against the cumulative schema. Stop and obtain an operator-owned
recovery plan for such a database; do not edit its migration history, renumber
its SQL, or apply lower versions out of order to manufacture a pass.

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
