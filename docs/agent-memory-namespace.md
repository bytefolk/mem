# Per-agent memory namespaces

Status: first runtime slice for [mem#227](https://github.com/bytefolk/mem/issues/227).
Composes with [durable-memory.v1](DURABLE_MEMORY.md) (#220) and context-pack
budgets (#71). It does not replace those contracts.

## Problem

Workspace-scoped recall can mix **executor** and **planner** conclusions in
one prompt. Hosts also dump whole namespaces. Operators need a small,
attributed pack from **this** agent's records, with an inspectable hit reason.

## Decisions (open questions from #227)

1. **Forgetting / expiry** — keep pin, archive, and permissioned forget on
   `POST /v1/memories`. Auto-deposit without pin stays recallable and is the
   first to archive. TTL on the `durable-memory.v1` envelope stays eligibility
   only; this slice does not add a new expiry column.
2. **Cross-agent share** — **isolate by default**. `GOAL.md` share-by-default
   applies to **hosts** continuing a workspace. Roles inside one workspace stay
   private unless the caller passes `extra_agent_ids` (explicit share). Full
   `durable-context.v1` grants remain the operator allowlist for principals;
   they are not implied by `agent_id`.
3. **k and token budget** — reuse `POST /v1/context` `limit` / `max_chars`
   (#71). Agent namespace filtering happens **before** ranking.

## Boundary: documents vs memory

| Store | Owns |
| --- | --- |
| `bytefolk/doc` and mem files/versions | Documents, editable files |
| `memories` rows | Agent conclusions, checkpoints, identity-bearing context |
| A memory row | May **point at** a file/doc via `source_file_id` / locator; it is not the document |

## Runtime slice

- `producer_agent` on `memories` is the namespace key (`agent_id` on the wire).
- `POST /v1/context` and `mem_context` accept `agent_id` and optional
  `extra_agent_ids` (max 8). When `agent_id` is set, lexical recall hard-filters
  `producer_agent` to that list **before** exact/FTS/trigram ranking.
- Omitting `agent_id` keeps today's operator browse (no producer filter).
- Hits already carry `reason` and `provenance` (`agent_id`, `task_id`, time).
- Auto-deposit is `mem_remember` with `kind=decision|task_state` and `agent_id`.
  Hosts call it at task end; mem does not invent a second chat runtime.

## Non-goals

Dump-all default recall. Using doc/file versions as the memory store.
Replacing #71 budgets or #220 grant receipts. HTTP for `durable-memory.v1`
before Gate D0.
