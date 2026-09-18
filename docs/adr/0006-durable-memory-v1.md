# ADR 0006: durable-memory.v1 is an additive principal-bound envelope

- Status: Proposed (contract only; runtime waits for RoleWeave Gate D0)
- Date: 2026-09-18
- Consumes: [mem#220](https://github.com/bytefolk/mem/issues/220),
  RoleWeave [#327](https://github.com/bytefolk/roleweave/issues/327) R1,
  P1 on [roleweave#345](https://github.com/bytefolk/roleweave/pull/345)

## Context

ADR 0001 stores immutable Agent occurrences. ADR 0003 adds pin, archive,
restore, and permissioned forget. `durable-context.v1` resumes those
occurrences through an explicit grant allowlist.

RoleWeave's memory plane needs a **derived** record for long-lived decisions
and preferences. The R1 draft used a free-string `scope`. That cannot
fail-closed isolate workspace, position principal, MemoryPort `memoryScope`,
and grant/revocation, and it cannot show why a receipt omitted a record.

Runtime implementation is blocked until Gate D0. The contract must still be
reviewable now so later PRs pin one schema.

## Decision

Ship `durable-memory.v1` as an additive envelope:

- Required `binding` (`workspace_id`, `position_id`, `principal`,
  `memory_scope`) instead of `scope`.
- Required `grant` that reuses `durable-context.v1` grant **ids** and points at
  `capability-grant.v1`. `grant.mode` is `read` only. `grant_version` is
  envelope-side. `revoked_at` and `permission_digest` are first-class.
- Eligibility treats expired, revoked, malformed, superseded, forgotten, and
  out-of-scope records as ineligible.
- Pin may preserve TTL eligibility and must not enlarge permission.
- TTL/expiry never implies physical deletion of the source log.
- Forget is a permissioned mem operation. The contract evaluator never
  reports a local fake delete.
- Exact readback compares the canonical envelope. Grant status always appears
  on the receipt.

No HTTP route, migration, or MCP tool is added in this change.

## Consequences

Positive:

- RoleWeave #345 P1 has a mem-side schema to pin.
- Cross-principal default deny is structural, not a convention on a path
  string.
- Receipts can display revocation without returning payload.

Trade-offs:

- Two versioned contracts (`durable-context.v1` and `durable-memory.v1`) until
  a later runtime PR projects one onto the other.
- Live E3 evidence is still required before claiming production recall.

## Rejected alternatives

### Keep `scope` as a free string and document the format

A path string cannot carry grant version, revocation, or permission digest.
Callers would invent parallel headers. That is the P1 defect.

### Implement HTTP now

AC-006 and RoleWeave Gate D0 forbid runtime consumption of an unaccepted
revision. A handler without an accepted parent design would be speculative.

### Treat pin as a permission upgrade

Pin is a ranking / TTL exception. Using it to bypass grants would leak
cross-principal memory.
