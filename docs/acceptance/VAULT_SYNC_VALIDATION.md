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
- Worker: N/A; no Worker, protobuf, model or queue behavior changed. The Web
  tooling follow-up and its complete validation are recorded below.
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

## CI baseline repair (2026-10-09)

The first PR run passed Go, PostgreSQL, Worker, existing Web browser acceptance,
deployment profiles, CodeQL and dependency review. Three gates failed for
unchanged baseline dependencies or external artifacts, before exercising any
Vault code:

- [Linux Web audit](https://github.com/bytefolk/mem/actions/runs/37806469015/job/113411946797)
  and [Windows audit](https://github.com/bytefolk/mem/actions/runs/37806469015/job/113411946574)
  both reported 14 high and 2 moderate findings in the original lockfile.
- [HTTP/CLI/MCP lifecycle](https://github.com/bytefolk/mem/actions/runs/37806468988/job/113411942810)
  stopped at an unauthorized pull of the original public MinIO image; no
  application service or migration had started.

This follow-up keeps the audit thresholds, all lifecycle assertions and CI
timeouts. It does not suppress vulnerabilities, ignore findings or disable a
check.

### Dependency cause and compatibility

The published [braces stack-exhaustion advisory](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm)
affects every available version through 3.0.3 and lists no patched version.
Tailwind 3.4.19 still requires chokidar 3, fast-glob and micromatch, which retain
that chain. typescript-eslint 7 also retains it through globby/fast-glob. These
are active CSS and TypeScript build tools, so removing an unused package or
applying a compatible braces patch cannot remove the finding.

Use official Tailwind 4.3.3 and its same-version PostCSS plugin, plus
typescript-eslint 8.71.1 (which supports the existing ESLint 8.57.1). Keep the
existing theme/config palette, font, animation and shadow definitions. Use
tailwind-merge 3.7.0 for its [supported Tailwind 4 class semantics](https://github.com/dcastil/tailwind-merge/blob/main/README.md).
Apply compatible lockfile patches for
[brace-expansion](https://github.com/advisories/GHSA-q2hr-2g5m-vwhr),
[source-map-js](https://github.com/advisories/GHSA-68fv-2mgg-jv7q) and the published
undici advisories surfaced by npm audit. No vulnerable package is renamed or
replaced by an unreviewed fork to hide its audit identity.

Follow the [official compiler upgrade contract](https://tailwindcss.com/docs/upgrade-guide):
the existing JS theme is loaded explicitly with `@config`, and compilation uses
`@tailwindcss/postcss`. Global compatibility declarations retain the existing
2px small radius, 4px backdrop blur, button cursor, placeholder defaults and
keyboard outlines. No React component or product flow is migrated.

| Verification | Result |
| --- | --- |
| Linux Node 24.11.1 / npm 11.17.0 `npm run audit` | PASS; zero vulnerabilities at original production-moderate and all-dependencies-high thresholds |
| Windows Node 24.19.0 / npm 11.17.0 `npm.cmd run audit` | PASS; both original thresholds and shell-free Windows invocation pass |
| `npm run lint`, `npm run typecheck`, `npm test`, `npm run build` | PASS; 109 unit tests; pinned token bridge checks 334 contrast pairs; production build succeeds |
| Fresh `npm ci` followed by `npm run audit` and `npm run build` | PASS; checked lockfile reproduces the zero-finding audit and same production assets |
| `npm run test:i18n`, `test:theme`, `test:enrichment`, `test:memory`, `test:transfer`, `test:managed-embedding` | PASS; all original assertions retained |
| New theme compatibility browser probes | PASS; dark/light RGB and opacity, radius/blur, pointer and 2px solid keyboard outline also verified under forced colors |

The token-source and generated-CSS validation copies were normalized to their
Git-equivalent LF bytes for Linux's pinned-byte check. Neither the upstream
token hash nor generated content was changed in the commit. The existing
production bundle-size warning remains visible.

### Lifecycle artifact repair

MinIO Community Edition's [official source-only distribution contract](https://github.com/minio/minio#source-only-distribution)
no longer provides the anonymously pullable historical artifacts used by this
test. Use the official
[MinIO source](https://github.com/minio/minio/tree/7aac2a2c5b7c882e68c1ce017d8256be2feea27f)
and [mc source](https://github.com/minio/mc/tree/77f82e18b5401a65958f1619df6ebb994634bd88),
pinning both commits and archive SHA-256 before extraction. Build with existing
Go using `-mod=readonly`, then place the binaries and their upstream licenses
into a fixed-digest official Alpine runtime image. A temporary Compose override
uses only the uniquely named test image, with `pull_policy: never`; the existing
test Compose graph and production deployment assets are unchanged.

| Verification | Result |
| --- | --- |
| `bash -n scripts/acceptance_agent_memory.sh` | PASS |
| Official ShellCheck 0.11.0 on the final script | PASS |
| Full `scripts/acceptance_agent_memory.sh` with the CI-exact `GOTOOLCHAIN=go1.25.0` | PASS; official-source build, fresh migration 27, healthy MinIO, real HTTP/CLI/MCP and PostgreSQL forgetting/redaction assertions all complete |
| Ownership cleanup | PASS; run-specific containers, networks, volumes, image, directory and port lock removed; production containers unchanged |

The local Windows-owned worktree used `GOFLAGS=-buildvcs=false` only while
executing its validation binary builds, because Linux Git cannot parse its
Windows `.git` pointer. The script's checksum-pinned upstream tarball builds
explicitly disable VCS stamping, rather than accidentally attributing those
sources to the enclosing checkout. The CI checkout, Go version, timeout and
all existing service and lifecycle assertions remain unchanged.

Remote CI on the repaired head must still rerun. Local pass is not substituted
for required remote checks, CODEOWNERS approval or resolved review conversations.
