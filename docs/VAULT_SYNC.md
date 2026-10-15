# Workspace Vault sync v1

Implements [mem #234](https://github.com/bytefolk/mem/issues/234).

This additive text-note service keeps logical identity independent of the
existing `/v1/files` content-deduplication model. It never changes that model,
invokes AI, or treats note properties as permissions.

All endpoints use existing Bearer authentication and workspace membership.
`X-Workspace-ID`/a bound token selects the workspace. A token must also cover
the canonical path `/Vaults/<vaultId>`.

* `GET /v1/vault/list` (`read`) returns
  `{ "vaults": [{ "vaultId", "title", "revision", "updatedAt" }] }`.
  Out-of-path Vault IDs are omitted, without revealing their count.
* `GET /v1/vault/snapshot?vaultId=<UUID>` (`read`) returns a full snapshot.
  A new Vault returns revision `0`, empty title, `updatedAt: null`, and `[]`
  without creating a row.
* `POST /v1/vault/commit` (`read` + `write`) accepts
  `{ "vaultId", "baseRevision", "title"?, "entries": [...] }`.
  Entries are **deltas**, not a replacement of the full Vault. An entry is
  `{ "noteId", "path", "content", "deleted"?, "properties"? }`.
  Unlisted notes remain unchanged. `deleted: true` requires empty content.
  Omitted properties retain existing metadata; a new note defaults to `{}`.
  Empty entries are allowed only with a title update.

Both snapshot and successful commit return:

```json
{
  "schemaVersion": "vault-snapshot.v1",
  "vaultId": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  "title": "Knowledge",
  "revision": 1,
  "updatedAt": "2026-10-07T12:00:00Z",
  "entries": [
    {
      "noteId": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
      "path": "岗位/空白笔记.md",
      "content": "",
      "revision": 1,
      "deleted": false,
      "properties": {}
    }
  ]
}
```

IDs are nonzero UUIDs. Vault IDs in requests use canonical lowercase form.
Paths are canonical relative POSIX paths up to 512 UTF-8 bytes, preserving
Chinese and spaces. Absolute paths, traversal, backslashes, control characters,
Windows-forbidden filename characters and trailing dots/spaces are rejected.
Two active notes cannot share a case-folded path. Logical notes with identical
content, including empty content, retain separate IDs. Text is UTF-8; only
newline, carriage return and tab control characters are accepted. Attachments
continue to use the existing mem file API and references rather than binary
data embedded in this text protocol.

One transaction locks and compares the whole Vault head. Successful commits
advance it exactly once and append content revisions; mutable head pointers
select current versions. Old entry revisions cannot be updated in place.
Delete creates a retained tombstone; it does not physically erase history.
Rename/swap changes logical paths while preserving note IDs.

* Stale head: `409 { "error": "vault_revision_conflict", "currentRevision": N }`.
* Active path collision: `409`, `error: vault_path_conflict`.
* Invalid body/path/identity: `400`, `error: vault_request_invalid`.
* Capacity/body/history limit: `413`, `error: vault_limit_exceeded`.
* Unavailable storage: `503`, `error: vault_unavailable`; success is not assumed.

Clients keep their own base snapshot and local manifest. On 409 they pull the
current head, merge nonoverlapping edits, and preserve conflicts as explicit
copies. A lost response is reconciled through snapshot readback, never a blind
overwrite. The service does not modify local files or promise a sync daemon.

Bounds: 4 MiB HTTP commit body, 1 MiB per note, 16 KiB per properties object,
256 UTF-8 bytes per title, 512 delta entries, 1000 heads **including tombstones**,
16 MiB current serialized content/path/properties, 64 MiB retained entry-history
content/path/properties, 10,000 commits and 64 Vaults per workspace. Limits
refuse atomically; snapshots are never silently truncated. A full snapshot may
require roughly 32 MiB on the wire due to JSON escaping; callers must use a
bounded Vault-specific response budget rather than an 8 MiB file-card budget.

Validation: unit/API tests are hermetic. `TestVaultSyncPostgres` requires an
explicit `MEM_TEST_DB` whose database name ends in `_test`; it verifies real
two-client races, distinct empty notes, workspace/auth isolation, tombstones,
immutable history, swaps, title CAS and additive migration down/up.

## Compatibility and portability boundary

The HTTP API is the canonical implementation. This initial additive surface
adds no CLI, MCP or standalone mem Web Vault editor; existing adapters and file
operations retain their contracts. Clients may synchronize ordinary Markdown
files and upload attachments through the existing file API.

The current workspace-bundle v1/v2 export/import formats do not contain Vault
heads or revision history. A Vault snapshot is a separate portable current-state
contract (docs/schemas/vault-snapshot.v1.schema.json). It has no auth tokens,
workspace membership or actor audit fields, and importing its properties never
grants permissions. Restoring a snapshot into an authorized target Vault uses
a normal CAS commit with the target head and a client-selected logical Vault
ID. Full history remains in PostgreSQL and requires an operator database backup;
this API does not claim to transport retained history between deployments.

Migration 0027 only creates new Vault tables and indexes. Downgrading to 0026
drops all Vault state and history, so preserve a database backup or current
snapshot before an operator downgrade. Existing files, auth and memory tables
are untouched. No login recovery, deployment credentials or model dependency
is introduced.
