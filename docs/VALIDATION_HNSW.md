# HNSW verification for #173 / #180

Migration `0025_ann_hnsw_indexes.sql` adds cosine HNSW indexes to text (768),
visual (512), and face (512) embeddings. Migration 0024 belongs to #194 (the
open draft that this branch is stacked on) and 0026 to #185. The integration
runner declares this branch's head as 25.

This branch includes #194's lexical migration 0024 and source commits.
Deployment must follow the [cumulative migration sequence](MIGRATION_SEQUENCE.md):
#194 → #197 → #195. #195 remains blocked behind this PR's unmet query acceptance.

Use PostgreSQL 16+ with pgvector, all migrations applied, and a populated
synthetic corpus in a disposable database whose name ends in `_test`:

```bash
bash scripts/verify_hnsw_indexes.sh "$MEM_TEST_DB" "$CORPUS_USER_UUID"
```

The read-only script validates the exact indexes and non-null vector counts
for the supplied corpus owner, and runs EXPLAIN ANALYZE for the text and visual
probe shapes. It checks each route's index name, propagates SQL errors, and
exits nonzero if ANY check fails — the three index-validity checks and the two
route probes all feed the same exit code.
It does not disable sequential scans or claim a production latency threshold.

## Verification ledger

**Execution entry point: none.** No CI job runs this script.
`grep -rn verify_hnsw .github/workflows` returns zero matches; the only CI
coverage `scripts/verify_hnsw_indexes.sh` receives is `bash -n` from the
"Validate project scripts" step in `.github/workflows/memory-validation.yml`,
which is syntax-only. That lint step's `shellcheck` invocation enumerates a
fixed file list that does **not** include this script, so the claim "PASS: shell
syntax" means parsed, not linted. Any result below therefore comes from a human
run, not from a green check.

### Runs recorded

| Date (UTC) | Head | PostgreSQL / pgvector | Outcome | Provenance |
| --- | --- | --- | --- | --- |
| 2026-09-11 | `d6b077fb51473a94d27d6dfd653fc6f1002a81b2` | 16.14 / 0.8.2 | `Results: 4 passed, 1 failed`, exit 1 — the three index-validity checks PASS, the visual probe PASSes, the text probe FAILs on a sequential scan | **Reported by the PR author** in the #197 description. Not re-executed here; not independently reproduced by this reviewer. |
| 2026-09-11 | `1d8fc821ab4fab038c7724414948df46b982515b` | 17.10 / 0.8.3 | Same shape, author-reported | Same provenance caveat. |
| — | follow-up commits on `fix/197-hnsw-review-blockers` | not available | **NOT EXECUTED** | The correcting commit had no PostgreSQL/pgvector instance and no Go toolchain in its environment, so it could neither re-run the script nor compile the changed test. It changed only prose, script labels and error handling. |

The head that CI currently builds (`e7cda235…` and later) has **no recorded run of
this script at all**. Treat "4 passed / 1 failed" as a historical, author-reported
observation whose reproduction on the repository-pinned `pgvector/pgvector:pg16`
image has not been demonstrated, not as this head's result.

### What CI does prove about these indexes

`server/internal/db/migration_sequence_test.go` asserts, inside the strict
23 → 24 → 25 populated upgrade, that three `hnsw` indexes exist with
`indisvalid = true`. That assertion proves **DDL validity only**:

- It is a planner-behaviour non-proof: it never runs `EXPLAIN`, so it says
  nothing about whether any query uses the index. The text route's own probe
  says the opposite.
- Each of the three tables now carries a seeded non-null vector row before
  version 25 builds its index. Without that seed, `indisvalid = true` on an
  empty table is trivially true and two of the three "valid index" results
  would be vacuous. One row removes the vacuity; it is far from a populated
  corpus and does not make the build cost representative.
- The index check asserts access method, index name and `vector_cosine_ops`,
  but never the indexed column or its `atttypmod`, so a wrongly-dimensioned
  column carrying a correctly-named index would still pass.

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
populated-table migration, not a measured face-query speedup. It has no consumer
at all, so it carries write amplification without a demonstrated benefit.

The visual PASS is bounded the same way: the probe reproduces only the
distance-leading `ORDER BY … <=> … LIMIT` skeleton with a `user_id` predicate.
It omits the `appendPathFilters` / `appendMIMEFilter` / `Since` / `Until`
predicates that `runVisualANN` adds, so a filtered visual plan may differ and is
**NOT VERIFIED** — despite this document requiring equivalence "including
existing authorization/path/MIME/time filters". Neither relator shape is probed:
`recomputeVisual` (same-event) is distance-leading but unverified, and
`recomputeText` (same-topic) leads with `e.file_id`, which makes a HNSW KNN scan
structurally unavailable. Four consumption sites are named by the code; the
script tests two, and one of those two fails.

Recall, real embedding quality, production latency, index build time and index
size are NOT VERIFIED. #175 / #184 track live retrieval benchmark evidence;
fixture tests are not live quality results. No numerical improvement or recall
percentage is asserted here.

Fixed `vector(768)` / `vector(512)` column types reject wrong dimensions on
insert, before HNSW construction. NULL vectors can remain in the tables but
are not indexed. Down removes only the three new indexes and retains data.

## Issue and PR linkage

These facts belong in a durable artifact because a PR description is editable and
does not survive a squash merge.

- **#173 is not closed by this change and must not be auto-closed.** Of its five
  acceptance criteria, only the ANN-index DDL criterion is satisfied here. Its
  second criterion — "a regression check proves the planner takes them: `EXPLAIN`
  for the text **and** visual query shapes shows an index scan … on a populated
  corpus" — is failed for text by this branch's own script, and no regression
  check runs it. The linkage is therefore `Refs #173`, never `Fixes #173`. Note
  that commit `d27a4936` in the preserved author chain still carries a
  `Fixes #173` trailer in its immutable message; GitHub acts on such trailers only
  when the commit reaches the **default** branch, and this PR's base is not the
  default branch, so merging as-is does not close #173. Anyone retargeting this
  stack onto `main`, or hand-editing a squash message from the chain, must strip
  that trailer.
- **#180 is CLOSED, unmerged and unverified.** It was closed 2026-09-10 as
  "superseded by #197, which covers the same changes **on current main**". That
  premise is false: #197's base is `codex/fix-pr-183`, the branch of draft #194,
  not `main`. #197's description still asserts "#180 remains open", which is
  stale.
- **#194 is a hard prerequisite, not an ordering preference.** `main` holds
  migrations 0001–0023. Landing 0025 there without 0024 opens a numbering gap,
  and `TestMigrationFilesContiguous` rejects a gap outright. The
  `MERGEABLE/CLEAN` state reported for #197 only holds against #194's branch.
  The stack order `#194 → #197 → #195` is currently undecidable on its own terms
  because #194 is `CONFLICTING`, so the floor of the stack cannot reach `main`.
