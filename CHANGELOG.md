# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
The project publishes 0.x prerelease versions; a stable release line is not yet published.

## [Unreleased]

### Added

- `mem put <path> --watch` one-way foreground directory watch: poll `--interval` (default 30s), ingest new files after one quiet interval, report changed files without re-ingesting, and never propagate local deletes. Refs #110.

### Changed

- The live recall producer (`python3 -m benchmarks.recall produce`) reads engine, profile, provider and embedding dimension from the running memd instead of CLI labels, scores the multilingual v1 set (English and Chinese, including image-description queries), and stays on-demand rather than a CI gate. Refs #175.
- Index generation HTTP create/activate/rollback stay `503 execution_unavailable` with `execution_wired: false` on that body, and the same flag is now on events as well as list/status/cancel/resume/discard. Create still returns `400` for malformed JSON or an empty `profile_id` before the 503. Empty-workspace activate is refused by the same 503. Refs #174.
- Ingest cursor locks try non-blocking exclusive locks and give up after 5s so a wedged peer becomes a warning instead of a silent hang. Refs #139.

## [0.1.2] - 2026-09-18

### Added

- Additive `durable-memory.v1` envelope for derived RoleWeave/mem records
  (`#220`). Isolation is workspace + position principal + `memory_scope` +
  grant/revocation (reusing `durable-context.v1` grants and
  `capability-grant.v1`); a free-string `scope` is rejected. Eligibility treats
  expired, revoked, malformed, superseded, forgotten, and out-of-scope records
  as ineligible. Pin does not enlarge permission. TTL/expiry is recall
  eligibility, not physical deletion of the source log. Forget stays
  permissioned and never a local fake delete. Grant status and
  `permission_digest` enter the readback receipt. Contract only: no HTTP,
  migration, or MCP wiring before Gate D0. Schema:
  `docs/schemas/durable-memory.v1.schema.json`.
- Cosine HNSW indexes on `embeddings_text` (768), `embeddings_visual` (512),
  and `embeddings_face` (512) via migration 0025 (`#173`). Text search walks
  `ORDER BY embedding <=> query LIMIT n` (planner-usable) and falls back to
  exact per-file `DISTINCT ON` when a bounded scan underfills after
  deduplication. Visual cosine-order queries can use the visual index. Face
  clustering remains in-process; the face index is DDL only. Recall is not
  claimed here; the live harness is `#175`.
- Advertise the model-free lexical file-search route in the MCP `mem_search`
  schema and verify `tools/list` plus route/filter forwarding through `tools/call`.

- `mem doctor` — a read-only diagnosis of why the CLI cannot talk to a working
  server (`#112`). It reports four checks in a fixed order: reachability of the
  configured server URL, whether a credential exists, the workspace the server
  resolved for that credential, and CLI/server version skew. Each finding carries
  the SPEC §7.1 exit code it contributes (`0` ok · `2` not_found · `3` auth ·
  `4` plan/quota · `5` provider/timeout), and a check that an earlier failure made
  impossible is reported as `skipped` instead of guessed. It issues only `GET`
  requests and never writes configuration, starts a container, or installs a
  dependency; `--format json` emits the `mem.doctor` v1 document described by
  `docs/schemas/mem-doctor.v1.schema.json`. A token is described only by where it
  came from, and a configured URL has its userinfo and its query parameter values
  replaced by `REDACTED` — a credential in a query parameter is the shape pgx
  accepts as a real password — or, when the URL cannot be proven to be a
  credential-free transport URL, is withheld whole. See `docs/DEPLOYMENT.md`.
- First-run guidance: a command that fails because no credential exists now says
  so on a machine with no configuration at all by naming the documented
  deployment path (`deploy/compose`, `docs/DEPLOYMENT.md`), instead of telling
  somebody to log in against a server that is not running yet. Hosts that already
  have a configuration keep the previous, shorter hint.
- File search gains a model-free lexical route (`route=lexical`). FTS + trigram
  over `files.name` — same tier shape as memory Recall — so a deployment with
  no embedding worker can still find files by name. CLI: `mem search "query"
  --route lexical`.
- Publish installable server binaries (`memd`, `mem-migrate`, `mem-healthcheck`,
  `mem`) alongside `mem-mcp` in the release workflow for `darwin/arm64`,
  `darwin/amd64`, `linux/arm64` and `linux/amd64`, with per-group checksum
  manifests (`#151`).
- `/v1/version` now exposes `version` (semver), `revision` (40-hex git commit)
  and `contract` (durable-context wire contract) as distinct fields, so clients
  can pin a revision or accept a version range without a third mechanism. Both
  the release workflow and the Docker image inject all three at build time
  (`#151`).
- Documented first-run path in `docs/DEPLOYMENT.md` that yields a reachable
  endpoint, a workspace, a token with write and recall scopes, and the
  corresponding durable-context grant (`#151`).

### Changed

- README onboarding now leads with the `deploy/compose` path as the recommended
  first-run experience (one-shot: `generate-env` → `compose up` → register →
  use CLI/MCP). The bare-metal development path (`scripts/dev_up.sh`) is
  demoted to a development-only subsection, and `docs/RUN_LOCAL.md` adds a
  platform-equivalence table covering macOS, Ubuntu/Debian and WSL2 (`#109`).
  `mem doctor` already names `deploy/compose` on a machine with no config.
- Migrate GitHub repository, Release, issue, badge, and raw-content coordinates
  to the canonical `bytefolk` organization.
- Rename the npm wrapper to `@bytefolk/mem-mcp@0.1.2` and the MCP registry
  identity to `io.github.bytefolk/mem-mcp`. New executable caches use
  `bytefolk/mem-mcp`; a matching version/platform in the old
  `fullstack-ai-infra/mem-mcp` cache can seed a separately verified copy.
  Old cache entries, including 0.1.1, are never changed or removed by this
  compatibility lookup. Explicit cache overrides keep their existing meaning.
  The old npm package remains available for rollback; migration does not
  unpublish it or change stored memories. Update host package arguments using
  the migration guide in `npm/README.md`.
  `npm/registry-identity.test.js` asserts the identifier against the
  repository coordinate the installer itself uses.
- Internal: the local ingestion mechanics used by
  `mem ingest qoder` — deterministic recursive transcript walk, per-path line
  cursors (atomic rename write, reset when a file is rewritten shorter), the
  `--dry-run` / `--limit` semantics, per-file degradation on an idempotency
  conflict, run-report aggregation and the closed failure-code vocabulary —
  moved out of `server/cmd/mem` into a new `server/internal/ingest` package
  (`#111`). The connector is now a thin call site that supplies the Qoder parser,
  the memory payload and the HTTP upload. Memories payloads, the stdout summary,
  and the cursor file format and location under `~/.mem/ingest/qoder` are
  unchanged, and a root already given as a canonical absolute path keeps the
  cursor keys and `Idempotency-Key` values it had before the move. A root given
  relative, or one reached through a symlink, is now identified by its canonical
  absolute path, so its cursor key and per-line `Idempotency-Key` differ from the
  pre-refactor spelling. The package is the shared core that `put --watch`
  (`#110`) consumes instead of writing a second state layer.

### Fixed

- Recursive folder delete now removes the corresponding objects from bucket
  storage after the database transaction commits (`#177`). Previously, the DB
  rows were deleted but the blobs remained orphaned in the bucket. The cleanup
  is best-effort: a failed object delete does not roll back the folder delete,
  and failures are logged at WARN level so the operator can see which keys
  remain. The folder service now accepts an optional `ObjectStore` and logger
  via `folder.WithStore` and `folder.WithLogger` options. Recursive delete
  also refuses when an active or archived memory outside the folder still
  cites a file in the tree via `source_file_id`, so blob cleanup cannot
  destroy a live citation through `ON DELETE SET NULL`.

### Security

- Normalize the client-declared MIME type of a stored file before deciding how
  to serve it: `GET /v1/files/{id}/content` now forces `attachment` for
  interpretable types in every spelling a browser accepts (`Text/HTML`,
  `text/html; charset=utf-8`, the RFC 9239 JavaScript aliases such as
  `text/x-javascript` and `text/javascript1.5`, and the whole registered font
  tree including `font/sfnt` and `font/collection`), serves a declaration it
  cannot bound as opaque `application/octet-stream` instead of echoing it back,
  and re-emits a usable declaration with its parameters intact, so a required
  `multipart/mixed; boundary=…` or `codecs=` still reaches the recipient.
  Download names are filtered with the same control-character policy the upload
  validator applies, covering C0, DEL, C1, the U+2028/U+2029 line separators and
  Unicode bidi controls.
- Add `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and
  `Content-Security-Policy: default-src 'none'` to every API response, ordered
  outside the CORS handler so a preflight reply carries them too.
- Make the web proxy the single authority for `X-Content-Type-Options`,
  `X-Frame-Options` and `Referrer-Policy` on every response it serves, with one
  `Referrer-Policy: no-referrer` instead of the `same-origin` it shipped before,
  which contradicted the API's own value and let both reach a client on the
  proxied path. The API's copies are hidden at the proxy rather than removed
  from the API, so a `memd` running without a proxy keeps its defense in depth.
  Cached assets carried none of the three: a local `add_header` for
  `Cache-Control` replaced the inherited set entirely, so the set is now
  restated in that location. `scripts/test_nginx_security_headers.sh` measures
  the headers off a running nginx, since neither failure mode is visible by
  reading the configuration.

### Fixed

- Follow the shared design language for reading, numeric and action alignment; generate the existing Web color variables from a pinned design-system token snapshot, and use a single consistent empty-state pattern. Refs #211.

- Improve web caption and status contrast in both themes, including tinted danger
  buttons, and center action labels, context menus, badges, dialog prompts, and
  overview/detail headings. Restore localized cancel/confirm labels when a
  confirmation dialog caller omits custom action text.

- Every MinIO image reference in the test stack, the local development stack and
  the self-hosted single-node Compose profile now resolves from `quay.io` instead
  of Docker Hub. MinIO stopped publishing container images in October 2025 and
  removed the `minio/minio` and `minio/mc` repositories from Docker Hub, so an
  anonymous `docker compose up` fails with `pull access denied for minio/minio,
  repository does not exist or may require 'docker login'`. Because `Validate
  Agent memory` → `HTTP, CLI and MCP lifecycle` is a required status context,
  that registry withdrawal blocked every pull request from merging (`#207`).
  `quay.io` still serves the exact digests pinned in `docker-compose.test.yml`,
  so no image bytes change: the digest-pinned test stack keeps its digests and
  the release-tagged deployment stack keeps its tags — only the registry host
  differs. `deploy/compose/compose.yaml` previously carried the same broken
  reference, so the documented self-hosted path would have failed on a cold
  host even though no workflow exercises it.
- CI Web job now runs unit tests (`npm test`), including audit retry regression
  tests. `npm run audit` retries recognized transient registry failures up to
  three attempts per threshold (two retries), with a 60-second limit per attempt
  and portable backoff. It starts npm through Node on Windows, fails immediately
  for vulnerabilities, unknown errors or incomplete runs, and echoes the output
  of every attempt it retries so a self-healing failure still leaves a trace.
  That transcript is uploaded as a downloadable artifact
  (`web-audit-transcript-<sha>`, 14-day retention) on the success and failure
  paths alike, and because the step merges stderr into stdout the artifact also
  carries the per-attempt diagnostics. A dedicated `Windows audit evidence` job
  runs the audit through `scripts/win-audit-verify.bat` on a real Windows runner
  and uploads `win-audit-transcript-<sha>`, so the helper is executed rather
  than only replayed against a stub by its fixture test.
- A configured URL that carries credentials in a shape `url.Parse` does not
  report as userinfo no longer reaches output. `admin:pw@host` parses as
  `Scheme="admin"` with the credential in `Opaque` and `User` unset, so an
  implementation that gates on `User != nil` echoes it verbatim. On this base it
  leaked from the CLI API client — at request construction and at all four
  `http.Client.Do` sites, which the previous error path did not cover — and from
  `memd`'s startup log line and its fatal log line, the last of which additionally
  carries third-party errors that embed a whole DSN. Both now route through one
  shared gate that redacts a value it can prove is a transport URL and
  **withholds the value whole** otherwise. It does not scrub credentials out of
  error text, which cannot be made tight: `url.Error` renders with `%q`, so a
  quote inside a password arrives escaped and a scanner that pairs quotes
  mis-pairs and replaces nothing. Withholding costs some diagnosability by design;
  why a request failed is still reported, and a DSN still names the parameters it
  sets — every query parameter *value* is replaced by `REDACTED`, including
  `?password=`, which pgx honours as the real password.
- Checksum manifest generation rejects existing output files, directories and
  symlinks without modifying their targets, including dangling symlinks, and
  will not publish a manifest that is missing a row for an expected asset.
- Release validation requires a release's CHANGELOG link to start at the
  preceding CHANGELOG release, and accepts a link to that release's own page
  only when no release precedes it. Checksum generation handles each asset path
  separately on GNU and BSD tools, including directories with spaces, rejects
  empty sets, and reports a failed asset listing as a failed listing.
- The release guard suites count manifest lines without `wc -l`, whose BSD
  implementation pads the count with blanks, and no longer need GNU
  `find -printf`.
- Add an opt-in file-search ranking producer with explicit request failures, conservative result identity mapping, and operator-declared configuration. Live provider quality remains separately unverified.

- The npm installer no longer aborts a concurrent first run on Windows. The
  per-asset cache lock previously treated only `EEXIST` as contention, but a
  contended `mkdir` on Windows may raise `EPERM` or `EACCES`, so a process
  waiting for the lock holder failed outright instead of retrying. The retry
  path now proves a lock can be inspected before treating those Windows errors
  as contention, preserves prompt failure for unrelated permission errors, and
  observes its deadline when a competing lock disappears during inspection.

## [0.1.1] - 2026-08-31

### Changed

- The Release workflow now accepts only an existing annotated tag whose exact
  commit is on `main`; manual dispatch resolves the full `refs/tags/...` name
  so a same-named branch cannot shadow it. The workflow validates every public
  version surface, builds and checksums the explicit six-platform asset set
  from that commit, and renders release notes from this versioned section.
  Assets first upload to a draft; the workflow verifies the exact seven
  non-empty remote assets before the final step can publish the GitHub Release.

### Fixed

- `mem ingest qoder` derives a transcript's checkpoint key, its per-line
  `Idempotency-Key` values and its project/session memory path from one canonical
  root identity. Previously the root was used exactly as the caller spelled it, so
  two working directories that each contained `sessions/p.jsonl` and shared one
  checkpoint directory collided on a single cursor: the second run saw an
  up-to-date checkpoint and posted nothing, silently dropping that store. A
  relative or symlinked root now re-keys its existing cursors, which replays those
  files once instead of skipping them. Checkpoint writes stage through a distinct
  temporary file per save and no longer rewind a checkpoint another run already
  advanced, so a slower run finishing second can neither fail on a shared staging
  name nor undo the faster run's progress.
- An ingest cycle that aborted on a rejected write tallied the failure under the
  network code whatever the server had answered, because the connector mapped the
  typed API error to a CLI error before the shared core could classify it. The
  core now sees the typed error and classifies authentication, plan, quota,
  provider and timeout responses correctly, while the SPEC §7.1 exit codes stay
  mapped at the command boundary that owns them. The same tally was wrong for
  local reads: a failed `open` returns a `syscall.Errno`, which satisfies
  `net.Error`, so an absent or unreadable transcript was reported as a network
  failure instead of `root_missing` / `read_denied`, and a run that aborted while
  reading recorded no failure at all. Both paths now classify before the transport
  default and the aborted cycle reports the code it died on.
- The npm installer now verifies the selected Release binary against the
  release's SHA-256 manifest before making it executable, rejects malformed or
  ambiguous manifest entries, verifies cached binaries, and removes partial or
  unverified files on failure.
- The npm wrapper no longer relies on a dependency `postinstall` script, which
  npm 12 blocks by default. The first explicit `mem-mcp` invocation performs the
  checksum-verified binary bootstrap, later invocations reverify and reuse a
  valid cache, all bootstrap diagnostics stay on stderr to preserve MCP stdout,
  and installer failures prevent the child process from starting. Runtime
  binaries now live in a user-writable, version-and-platform-scoped cache rather
  than the installed package; an atomic per-asset lock prevents concurrent
  installers from deleting each other's verified result, stale recovery cleans
  only its owner's artifacts, manifest failures remove any unverifiable service
  path, and wrapper shutdown forwards signals with bounded escalation so the
  native MCP child is not left orphaned.
- Release validation now verifies that every immutable GitHub Action pin used
  by the release workflow resolves in the action's official repository, runs
  on the macOS system Bash 3.2, and has a deterministic compatibility gate
  against Bash 4-only collection builtins.

## [0.1.0] - 2026-08-30

### Added

- MCP distribution packaging: smithery.yaml, npm wrapper, README Tools table,
  and mcp-server repository topic (preparation for MCP Registry, Smithery,
  mcp.so, Glama, and PulseMCP discovery).
- `mem ingest qoder` — a local-artifacts connector that makes AI-agent
  conversation transcripts (Qoder/CLI session stores, `~/.qoder/projects/**/*.jsonl`)
  a first-class mem input source (`#103`). It normalizes each conversation turn
  into a memory `observation` with Qoder source flags, producer session/model,
  and message timestamp, and writes through the standard `/v1/memories` API so
  records are recallable from the API/MCP/CLI/UI unchanged. Ingestion is
  incremental (per-file line cursors under `~/.mem/ingest/qoder`, reset when a
  transcript is rewritten shorter) and idempotent (stable `Idempotency-Key` per
  file+line; a conflict degrades to skipping that file instead of aborting the
  run). The tolerant JSONL parser skips unparseable or empty-content lines
  rather than failing a run; a `--dry-run` mode plans without writing and leaves
  no checkpoint behind, so a previewed transcript stays ingestible.
  See `docs/integrations/qoder-ingest.md`.

- `merge_conservative` workspace bundle restore: importing a validated
  bundle into an existing, possibly non-empty workspace now compares every
  bundle object against the target under the import lock by stable identity
  and content hash, inserts only absent objects, skips identical or
  already-present content, and reports divergent objects as structured
  conflicts without ever overwriting target state. A durable per-object
  ledger (migration 0023, `workspace_import_objects`) records each decision
  in the same transaction as the merged state, so retried merges are
  idempotent and replay the exact inserted/skipped/conflict summary;
  conflict sets beyond the bounded detail budget abort the whole merge
  without writing anything. Available through the existing import API
  (`mode=merge_conservative`) and advertised in workspace capabilities.
- Web UI for the immutable memory correction/supersede relations landed in
  #90: memory list rows carry a server-derived `superseded` marker, detail
  and expanded ledger views show a bidirectional relations panel with peer
  resolution that degrades gracefully for unreadable peers, and a dialog
  creates `supersedes`/`corrects` edges against listed or manually entered
  peers with cache invalidation across lists, details, and relation panels.
  Relation listing now enforces anchor visibility (path authorization,
  not-found, and forgotten semantics mirroring `Get`), cycle detection
  traverses the supersedes/corrects DAG forward and treats idempotent
  replays as non-cycles, and bilingual `memories.relations.*` copy plus
  deterministic mock fixtures and component tests cover the panel's
  loaded/empty/error states.
- Version-pinned scoped durable-context contract (`durable-context.v1`,
  mem#70 REQ-001): explicit workspace-scoped recall grants with audit
  retention and idempotent soft revoke, read-only recall/get endpoints and a
  `mem_durable_context_recall` MCP tool that resume only granted active
  memories with version-pinned locators and provenance, a denied/stale/
  forgotten/unavailable error taxonomy, pinned-contract rejection of
  incompatible versions, and PostgreSQL scenario coverage for cross-session
  resume, cross-principal/workspace denial, superseded/forgotten/unapproved
  exclusion, and revoke/re-grant audit. Grants remain target-local policy and
  are not bundled in workspace exports.
- Additive versioned index-generation foundation with immutable per-route
  provider/model/dimension/pipeline identities, complete historical profile
  snapshots, exact corpus membership with deletion tombstones, tokenized
  expiring Worker attempts, workspace-consistent foreign keys, content-hash
  target progress, exact-dimension vector storage, privacy-bounded lifecycle
  audit, fail-closed ready/activation/rollback semantics, retained discard with
  physical expiry cleanup, and read-only API/CLI status surfaces. Worker rebuild
  execution and generation-aware ANN search remain explicitly unwired.
- Production deployment profiles: a secret-generated, loopback-only
  single-node Compose stack for Web, memd, Worker, PostgreSQL, Redis and MinIO;
  a multi-node Helm chart with horizontally scalable Web/Worker, single-replica
  non-overlapping memd rollouts, external state services, single-run migration,
  probes, disruption/topology controls, optional Web/Worker HPA and Ingress,
  NetworkPolicy and existing-Secret integration; model-free Worker images with
  ASR/CLIP/face extras opt-in; plus backup/restore, operations guidance and
  continuous Compose/Helm/production-image validation in CI.
- Complete Web Chinese/English localization with a persisted runtime selector,
  locale-aware metadata and display formatting, dictionary parity and
  hard-coded-prose auditing, and bilingual browser acceptance coverage.
- Persistent Web dark/light theme switching with pre-render application,
  localized accessible controls, theme-aware notifications, and browser
  acceptance coverage.
- Server-owned, workspace-scoped AI profiles: an offline-first
  `local-fast-v2` fixed to Ollama Qwen3 Embedding 0.6B at 768 dimensions, and
  a SaaS-only `idealab-quality-v2` fixed to an explicitly bound Idealab
  `text-embedding-3-large` plus Qwen3.7 Max, with explicit text/PDF stage
  contracts at profile revision `2026-07-30.1` / pipeline revision
  `file-enrichment-v2`, profile-aware CLI/API selection, preflight probing,
  per-stage usage receipts, crash-recoverable transactional settlement, and no
  implicit model download or provider fallback.
- Authenticated hosted memd-to-Worker execution with deterministic request and
  response HMACs, shared Redis replay protection, exact-provider startup
  readiness, fetched-content SHA verification before managed egress, and
  private/BYOM-compatible deployment classification.
- Repeatable stdio MCP certification for OpenClaw, Hermes Agent, Claude Code,
  OpenCode, and Codex, with versioned config manifests, a model-free fake-memd
  lifecycle/failure contract, isolated real-host evidence, and explicit
  `REGISTERED`/`DISCOVERED`/`INVOKED`/`NOT RUN` grading.
- Payment-provider-neutral workspace entitlements for optional managed
  embeddings, with atomic quota reservation, safe idempotent replay,
  indeterminate-outcome reconciliation, a read-only status API, and Web
  plan/quota/error presentation.
- Managed search/context idempotency support across CLI, MCP, and Web while
  preserving subscription-free private and local/BYOM providers.
- A versioned, synthetic Chinese/English recall benchmark with a deterministic
  lexical reference, provider-agnostic ranking import, checked-in baseline,
  per-slice quality/latency metrics and a fail-closed forbidden-source gate.
- A versioned, vendor-neutral local embedding catalog and `mem model
  list|recommend|install|activate` flow with hardware/runtime checks, explicit
  selection, pinned Ollama artifact integrity, and separate activation.
- Provenance-aware asynchronous file enrichment: bounded phone/device capture
  time and location, sanitized EXIF/media observations, reviewable AI
  description/tag suggestions, API/Web/CLI/MCP accept/reject controls,
  portable decisions, and provenance-aware CLI/MCP upload adapters.
- Workspace bundle v2 enrichment provenance with strict stable-key/source
  projection validation and read compatibility for historical v1 archives.
- Versioned `mem.handoff` v1 checkpoints, optimistic head comparison,
  deterministic `resume`, and task/checkpoint list/get inspection across API,
  CLI and MCP.
- Portable workspace bundle v1 with manifest, seven typed indexes, immutable
  checkpoint payloads, content-addressed blobs, checksums and dependency
  validation.
- Resource-bounded workspace export and empty-target `fresh` import across
  API, typed client, CLI and Web, including idempotent import ledger,
  structured/truncated conflicts and failure compensation.
- Read-only workspace import history: a bounded, paginated
  `GET /v1/workspaces/current/imports` ledger endpoint (owner/admin,
  unrestricted-path gated) projecting committed import ledger entries with
  bundle id, archive SHA-256 digest, schema version, restore mode, result
  status, conflict/skip counts and import time, plus a reverse-chronological
  expandable import-history block on the Web Workspace Transfer page with
  loading/empty/error states and bilingual copy.
- Web Drive trust surfaces for Tasks, checkpoint/Resume, Memories lifecycle
  control and Workspace Transfer.
- Real-image visual regression coverage and an opt-in multilingual CLIP
  ranking gate with an explicit checked-in baseline report.
- Model-independent structured Agent memories with provenance, stable
  `mem://memories/<id>` citations and PostgreSQL lexical recall.
- Idempotent `POST /v1/memories`, scoped `GET /v1/memories/{id}`, CLI
  `mem remember` / `mem memory` and MCP `mem_remember` / `mem_memory_get`.
- Authorization-bound cursor pagination and a bounded-summary memory ledger.
- Auditable memory feedback (`useful`, `not_useful`, `pin`, `unpin`),
  optimistic archive/restore transitions and retry-safe forgetting across the
  API, CLI, MCP and Web control surfaces.
- Context Packs that can recall files, structured memories or both through
  `source=all|file|memory`, with kind filters and context-size budgets.
- Explicit partial-retrieval warnings when one Context Pack lane fails.
- Architecture decision record for immutable memory occurrences.
- `mem auth status` for verifying the current token and reporting workspace
  access.
- Reproducible, project-specific validation for Go, Worker, Web, PostgreSQL
  migrations/race paths and isolated process-level HTTP/CLI/MCP acceptance,
  backed by repository CI.
- Issue-first contribution, triage, review, security, and community governance
  standards.
- Pull request policy and CI jobs for Go, the Python worker, and the Web
  application.
- Go and Python coverage artifacts plus verified Go, Python, and Web build
  artifacts.
- Checked-in Go and Python protobuf stubs for reproducible fresh-clone builds.
- Additive durable-context grant allowlist view fields:
  `GET /v1/durable-context/grants` now returns each grant's `memory_status`
  plus a derived `status` (`active`/`revoked`/`superseded`/`forgotten`,
  revocation wins over later memory lifecycle changes), and capabilities
  expose a `permissions_manage` flag for the admin scope. Grant rows, query
  semantics, and the idempotent soft-revoke response are unchanged.
- Admin-gated Permissions page in the Web UI: issued agent tokens and browser
  sessions with scopes, path restriction, creation/last-used timestamps and
  revoke; durable-context recall grants with principal, workspace, lifecycle
  status and grant/revoke audit, revocable through the existing idempotent
  soft revoke; confirmation dialogs for destructive revokes and complete
  bilingual loading/empty/error/forbidden states.

### Changed

- Use the platform-native sans-serif stack consistently in development and
  production so the Web UI never depends on a third-party font request.
- Preserve the published `local-fast-v1` and `idealab-quality-v1` snapshots
  exactly for enabled persisted workspaces while hiding them from new
  selection; one SaaS process can authenticate and account for both exact
  managed generations during migration, V2 owns the revised text/PDF-only
  contract, and any populated V1→V2 switch now requires a versioned generation
  rebuild.
- Serialize managed result/outbox commits with stale reconciliation and period
  rollover, safely terminalize late file state, and scrub outbox file/content
  identity when the source file is deleted.
- Ollama text embeddings now use one modern batched `/api/embed` request with
  an explicit 768-dimensional contract and fail closed on batch or dimension
  mismatches.
- Checkpoint history lists now return bounded summaries; full handoff payloads
  and evidence references require an explicit checkpoint get or resume.
- Retired the built-in ask/chat path. mem now returns evidence while the
  calling Agent owns reasoning and answer generation.
- `mem_context` now defaults to `source=all`, so evidence may identify a
  structured memory instead of carrying `file_id`. Existing file-only clients
  should request `source=file`; other clients should branch on
  `source_kind/source_id`.
- Bound Agent tokens and retrieval to a workspace and canonical path scope.
- Track the actual embedding provider used by an index and fail closed for
  legacy vectors whose provider cannot be proven.
- Namespaced login, logout and token management under `mem auth`; legacy
  top-level paths remain hidden compatibility aliases with deprecation
  warnings.
- Inherit organization-wide contribution, issue, pull-request, conduct, and
  support defaults from `bytefolk/.github`; keep only `mem`-specific
  development, security, triage, ownership, release, and validation rules in
  this repository.
- Align pull-request policy with the inherited controlled exceptions for
  trusted Dependabot updates and maintainer-labeled security advisories.
- Removed the repository-specific cloud-model credential and model-probing
  helpers. Core Agent memory remains model-independent; optional indexing
  providers stay behind the Worker contract.

### Security

- Upgrade the Web runtime to React 19.2 and React Router 8, remove the retired
  `react-router-dom` compatibility package, refresh vulnerable transitive
  tooling dependencies, and make production-moderate/development-high npm
  audits required in CI, with a documented per-advisory reachability review.
- Fail closed in production on development state-service credentials,
  automatic per-replica migrations, open registration, wildcard CORS or an
  unauthenticated Worker.
- Make profile IDs the only client-selectable AI-routing input: reject model,
  endpoint, credential, and stale-profile injection; validate 768-dimensional
  embedding responses; reserve managed usage before a declared paid stage; and
  fail closed rather than silently falling back across a billing or data-egress
  boundary.
- Authorize account, workspace membership, scope, and path before entitlement
  lookup or provider invocation; persist only bounded identifiers, accounting
  state, timestamps, and hashes in the managed-embedding usage ledger.
- Fail closed for SaaS entitlement readiness and prevent provider fallback,
  duplicate charging, or raw upstream error leakage after timeout and
  indeterminate outcomes.
- Reject hidden-reasoning wrappers and nested JSON-like model values across
  Worker, server, database, and workspace-bundle boundaries; keep raw provider
  errors and malformed processor facts out of persistence, APIs, and bundles.
- Remove accepted model tags from the legacy effective projection before an
  enrichment downgrade, preventing a later re-up from reclassifying them as
  user-authored tags.
- Exclude credentials, tokens, provider secrets, runtime state and derived
  indexes from workspace bundles; carry only hashed memory idempotency keys.
- Bound transfer time, archive bytes, expanded metadata/records and concurrent
  operations; use `0700` spool directories and `0600` temporary files.
- Keep duplicate-content file entries independent after restore so deleting
  one object key cannot break another file.
- Derive workspace, actor and token provenance on the server instead of
  trusting client-supplied identity.
- Hide absent and out-of-scope memories behind the same not-found contract.
- Bound remember request/content/metadata fields and reject unknown JSON
  fields, malformed source hashes and idempotency-key conflicts.
- Prevent folder deletion from silently deleting active or archived memories.
- Re-authorize the persisted path on idempotent memory replay, preventing an
  old path and key from exposing a record after its folder is moved.
- Require compare-and-swap state versions and hashed idempotency keys for
  memory writes; forgotten records clear their payload, path, creator and
  request fingerprint, retain only a generic retry-safe tombstone, and never
  re-enter list or recall results.
- Escape terminal control/bidirectional sequences in human-readable memory
  output and reject them in new virtual paths.
- Return bounded feedback/lifecycle control projections so MCP mutations
  cannot echo full untrusted memory payloads into an Agent context.
- Prevent checkpoint-list pages from amplifying up to 200 complete handoff
  payloads and reference arrays into one Agent context.

### Fixed

- Close the `mem-mcp` distribution gaps that made the npm wrapper installable on
  only three platforms: the release matrix excluded windows and `linux-arm64`,
  the package `os` field omitted `win32` so npm rejected Windows installs with
  `EBADPLATFORM`, and the `mem-mcp` bin wrapper was a bash script that npm's
  Windows cmd/ps1 shims cannot execute. All six published platform targets are
  now covered, the wrapper is a Node script, and the platform-to-asset mapping
  that `install.js` and the wrapper each carried separately now lives in one
  table in `npm/platforms.js` so the two cannot drift again.
- Make combined file and folder move-plus-rename requests atomic, including
  validation, final-path conflict handling and destination folder creation.
- Keep rejected/superseded AI descriptions out of file detail, visual-search
  snippets, and workspace bundle projections; serialize same-file index runs
  and preserve the last usable text embedding when a partial retry produces no
  replacement.
- Preserve uploaded workspace objects after an indeterminate database commit
  and expose a stable `503` recovery contract requiring the exact same bundle.
- Serialized folder prefix mutations with folder, file, memory and checkpoint
  writers so concurrent renames cannot split a subtree or leave a file path
  pointing at a differently named folder.
- Made `same_person` ranking use directional source-person coverage and stable
  file-ID tie-breaking so a full person-set match ranks ahead of a partial
  match and equal candidates are selected deterministically.
- Align the Worker protobuf/gRPC runtime floors with the checked-in generated
  stubs and verify that contract in regression tests.
- Generate Go protobuf stubs directly into their destination instead of
  deleting a repository-root `github.com/` directory after generation.
- Preserve the primary Web acceptance failure when browser or Vite cleanup
  also fails.

[Unreleased]: https://github.com/bytefolk/mem/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/bytefolk/mem/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/bytefolk/mem/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/bytefolk/mem/releases/tag/v0.1.0
