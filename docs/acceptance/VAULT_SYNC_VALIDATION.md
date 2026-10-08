# Vault sync validation ledger

Issue: [mem #234](https://github.com/bytefolk/mem/issues/234).
Base: `c5f3fd591973dfaf0e36baecfee8cb8295c1bcb7` (`origin/main`).
Validation date: 2026-10-08. This ledger records implementation-side evidence;
it is not independent review approval or a deployment/release claim.

Environment: Linux amd64 through WSL, Go 1.25.0, PostgreSQL 16 with the CI-pinned
`pgvector/pgvector@sha256:00ba258a66dac104fd5171074a0084462a64a1369d8513f3d0a634e2f24d15bc`
image. A new loopback-only container used tmpfs PostgreSQL data and the synthetic
control database `mem_vault_publish_test`. Each integration-runner phase created
and removed its own nonce-marked `mem_verify_*_test` database. No deployed
database, credentials, container or user configuration was read or changed.

| Check | Exact command (repository root unless specified) | Result |
| --- | --- | --- |
| Server/CLI/MCP compilation | `(cd server && env -u MEM_TEST_DB go build ./...)` | PASS |
| Go static analysis | `(cd server && env -u MEM_TEST_DB go vet ./...)` | PASS |
| All hermetic Go regressions | `(cd server && env -u MEM_TEST_DB go test -count=1 ./...)` | PASS; database tests excluded here and verified separately |
| High-risk hermetic race checks, including Vault | `bash scripts/verify.sh race` | PASS; no data race |
| Fresh schema, strict populated 23 → 27 upgrade, historical rollback round trips, HNSW checks and all required PostgreSQL tests | `MEM_TEST_DB=<isolated control DSN> bash scripts/verify.sh integration` | PASS; migration head 27; no required test skipped |
| All required PostgreSQL tests under race detector | `MEM_TEST_DB=<isolated control DSN> bash scripts/verify.sh integration-race` | PASS; no required test skipped or data race |
| Whitespace and schema serialization | `git diff --check`; Python `json.load` on `docs/schemas/vault-snapshot.v1.schema.json` | PASS; JSON parsing is not a claim of full JSON Schema conformance testing |

The initial Windows worktree had CRLF shell/golden-fixture copies. The first
Linux attempt failed before integration setup (`pipefail\r`) and the Go doctor
golden comparison rejected line endings. Only those validation working copies
were converted to the Git-equivalent LF content; the commands above were rerun
and passed. No unrelated fixture, shell content or file-mode change is included
in this issue's diff.

## Observable Vault acceptance

`TestVaultSyncPostgres` passed in both normal and race integration runners:

- separate UUID identities preserve two independently synchronized empty notes;
- two actual clients writing the same base revision have exactly one winner
  and one `409` conflict; the losing edit never overwrites the winner;
- real router authentication, workspace selection and read/write scopes deny
  reader writes and keep another workspace's same Vault UUID isolated;
- successful commits append immutable old text, advance the head once, retain
  deletion tombstones, support path swaps and use the same CAS for title changes;
- real discovery lists the workspace's Vault, and real HTTP stale writes return
  the observed `currentRevision`;
- migration `27 → 26 → 27` is exercised only on disposable data, preserving
  existing file/auth schemas while explicitly dropping Vault state on downgrade.

Hermetic `internal/vaultsync` and `internal/api` tests additionally cover
malformed identity/path/text/properties, distinct note identity, deterministic
delta planning, stale heads, active-path collisions, count/byte bounds,
tombstones, unavailable storage, strict HTTP bodies, anonymous denial and
token-path filtering for both snapshot and discovery. Property values are
untrusted data and do not add workspace membership or tool authority.

## Compatibility and honest limits

- API: additive canonical HTTP routes; existing file/blob identity is unchanged.
  The new note ID is independent of content SHA-256.
- CLI/MCP/Web: N/A for new Vault UI/commands in this backend change. Their
  existing behavior is covered by the complete Go suite where applicable;
  no separate Vault adapter or desktop sync daemon is claimed.
- Worker/Web build/browser suites: N/A; no Worker, React Web, protobuf, model or
  queue behavior changed in this scoped server addition.
- Provenance/privacy: public snapshot contains current note data/properties,
  with no authentication material, actor audit IDs or new authorization model.
- Retry: a stale or uncertain commit is reconciled through current-head
  readback; clients retain their base and preserve divergent edits as copies.
  No request-level idempotency key or server-side automatic merge is claimed.
- Recall warnings: N/A; this surface does not change search, recall or ranking.
- Portability: `vault-snapshot.v1` transports current state. Workspace bundle
  v1/v2 does not include Vault state; retained history needs database backup.
- Deployment: NOT VERIFIED by this publication worktree; no production
  migration, restart, credential issuance, release or hosted-service rollout ran.
