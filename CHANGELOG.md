# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
The project is not yet publishing stable semantic-versioned releases.

## [Unreleased]

### Added

- Versioned `mem.handoff` v1 checkpoints, optimistic head comparison and
  deterministic `resume` across API, CLI and MCP.
- Portable workspace bundle v1 with manifest, seven typed indexes, immutable
  checkpoint payloads, content-addressed blobs, checksums and dependency
  validation.
- Resource-bounded workspace export and empty-target `fresh` import across
  API, typed client, CLI and Web, including idempotent import ledger,
  structured/truncated conflicts and failure compensation.
- Web Drive trust surfaces for Tasks, checkpoint/Resume, Memories lifecycle
  control and Workspace Transfer.
- Real-image visual regression coverage and an opt-in multilingual CLIP
  ranking gate with an explicit checked-in baseline report.
- Model-independent structured Agent memories with provenance, stable
  `mem://memories/<id>` citations and PostgreSQL lexical recall.
- Idempotent `POST /v1/memories`, scoped `GET /v1/memories/{id}`,
  `mem remember` and MCP `mem_remember`.
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

### Changed

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

### Security

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
