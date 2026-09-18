# Durable Memory (`durable-memory.v1`)

Status: additive contract for derived, grant-scoped memory. Pins RoleWeave
[#327](https://github.com/bytefolk/roleweave/issues/327) R1 and the P1
principal/grant correction on
[roleweave#345](https://github.com/bytefolk/roleweave/pull/345).
Requirement: [mem#220](https://github.com/bytefolk/mem/issues/220).

This document does not change runtime behavior. HTTP handlers, migrations,
and MCP tools wait for Gate D0. Live E3 evidence is required before a later
runtime PR may claim recall, forget, or grant enforcement in production.

## Why this envelope exists

`durable-context.v1` resumes **already stored** structured memories through an
operator-owned allowlist. `durable-memory.v1` is the **derived record** that
RoleWeave, digital-employee `MemoryPort`, and mem share for long-lived
decisions, preferences, workflows, and negative signals.

A free-string `scope` cannot express fail-closed isolation. Every record binds
all four of:

1. mem `workspace_id`;
2. position principal `position.<position_id>`;
3. canonical `memory_scope` (`/workspaces/<instance>/positions/<position_id>`);
4. a grant/revocation tuple (`grant_id`, `grant_version`, `permission_digest`,
   `revoked_at`) that reuses `durable-context.v1` grants and
   `capability-grant.v1`.

Cross-principal access is denied by default. Pinning cannot enlarge that
boundary.

## Contract rules

- The wire contract is pinned: `contract=durable-memory.v1`. Any other value
  is unsupported.
- Recalled text is `trust=untrusted` and `authority=none`. It cannot grant
  tools, identity, or instructions.
- Expired, revoked, malformed, superseded/archived, forgotten, and
  out-of-scope records are not eligible for recall.
- `expires_at` / TTL decides **recall eligibility**. It does not physically
  delete the source log, segment, or originating memory occurrence.
- Pin may keep an expired record eligible. Pin does not restore a revoked
  grant, a forgotten payload, or another principal's record.
- Forget is permissioned (`delete` token scope plus a workspace role that
  allows deletion) and is executed by mem. A caller must not treat a local
  cache drop as success. Failure is a visible `forget_denied`.
- Exact readback compares the canonical envelope. A digest, `state_version`,
  binding, or text drift is a mismatch, not a silent resume.
- Digests are full `sha256:` + 64 lowercase hex. Placeholders such as
  `sha256:ab` are malformed.

## How grant and revocation enter readback / receipt

Recall of one record returns a receipt, not a bare string:

```json
{
  "contract": "durable-memory.v1",
  "memory_id": "22222222-2222-4222-8222-222222222222",
  "locator": "mem://memories/22222222-2222-4222-8222-222222222222@1",
  "state_version": 1,
  "eligible": false,
  "omit_reason": "revoked",
  "pinned": true,
  "grant": {
    "grant_id": "33333333-3333-4333-8333-333333333333",
    "grant_version": 1,
    "mode": "read",
    "status": "revoked",
    "permission_digest": "sha256:98056de97087164dd9e0f5235cba6019d9576230faa1e37b104e735e5b5729a6",
    "revoked_at": "2026-09-18T11:59:00Z"
  },
  "readback": null
}
```

| Receipt field | Source | Why it is here |
| --- | --- | --- |
| `grant.grant_id` | `durable-context.v1` allowlist row | Ties recall to an operator-owned grant, not a path string |
| `grant.grant_version` | that row's revision | Detects re-grant after revoke |
| `grant.permission_digest` | canonical (workspace, principal, memory_scope, mode, version) | Detects grant tuple drift |
| `grant.status` / `revoked_at` | soft revoke | Makes denial auditable; UI must not show a payload |
| `readback` | exact stored envelope | Present only when eligible |
| `omit_reason` | eligibility evaluator | `expired`, `revoked`, `malformed`, `superseded`, `forgotten`, `out_of_scope` |

`capability-grant.v1` remains the digital-employee capability document. This
envelope stores a normative pointer (`schema_version=capability-grant.v1`,
`server=mem`); it does not reimplement grants.

MemoryPort continues to hold no grant/revoke/forget methods. Operators
provision tokens and grants on mem's admin surface. RoleWeave UI may request
forget; only mem may ack it.

## Eligibility order

1. Malformed contract, digest, principal, or binding.
2. Workspace / principal / `memory_scope` mismatch (default deny).
3. Revoked grant.
4. Forgotten payload.
5. Superseded or archived lifecycle.
6. Expired `expires_at` unless pinned.
7. Else eligible; emit exact readback.

## Relationship to existing mem APIs

| Surface | Owns | Does not own |
| --- | --- | --- |
| `POST /v1/memories` and lifecycle | Occurrence storage, pin as ranking, archive/restore, permissioned forget | This envelope |
| `durable-context.v1` | Explicit read grants per `(workspace, principal, memory)` | Derived RoleWeave kinds |
| `durable-memory.v1` | Versioned derived envelope + eligibility + receipt | Transcript warehouse, Host resume, HTTP (until D0) |
| digital-employee `MemoryPort` | Write/readback/recall seam, env-referenced token | Grant administration |

## Schema and example

- [`docs/schemas/durable-memory.v1.schema.json`](schemas/durable-memory.v1.schema.json)
- [`docs/examples/durable-memory.v1.example.json`](examples/durable-memory.v1.example.json)
- Evaluator: `server/internal/durablememory`

## Non-goals

- Runtime HTTP, SQL, or MCP wiring before Gate D0.
- Treating summaries as authority.
- HNSW / vector retrieval ([#173](https://github.com/bytefolk/mem/issues/173)).
- Transcript warehouse or Host resume handles.
