-- +goose Up
-- Data-plane hygiene from the index audit (#178).
-- Three independent fixes bundled into one migration because each is a single
-- DDL statement and none warrants its own schema version.

-- Item 1: embeddings_text uniqueness on (file_id, chunk_index).
-- The write path (indexer.go) DELETEs all chunks for a file before re-inserting,
-- so duplicates should never exist in practice. Deduplicate defensively before
-- adding the constraint: if any duplicates survived, keep the row with the
-- smallest id (earliest insert).
-- +goose StatementBegin
DELETE FROM embeddings_text
 WHERE id NOT IN (
    SELECT DISTINCT ON (file_id, chunk_index) id
      FROM embeddings_text
     ORDER BY file_id, chunk_index, id
 );
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE embeddings_text
    ADD CONSTRAINT uq_embeddings_text_file_chunk UNIQUE (file_id, chunk_index);
-- +goose StatementEnd

-- Item 2: partial indexes for ON DELETE SET NULL lookups on memories.
-- Without these, every file or user delete takes a RowExclusiveLock on memories
-- and performs a sequential scan to find the rows to null out.
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_memories_source_file_id
    ON memories (source_file_id) WHERE source_file_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_memories_created_by_user_id
    ON memories (created_by_user_id) WHERE created_by_user_id IS NOT NULL;
-- +goose StatementEnd

-- Item 3: memory_relations FKs must cascade with memories.
-- memories.workspace_id is ON DELETE CASCADE, so a workspace delete removes
-- memories rows. Without matching cascade on memory_relations, the delete then
-- fails on any edge touching those memories. Align the referential actions.
-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_workspace_id_fkey,
    DROP CONSTRAINT IF EXISTS memory_relations_source_id_fkey,
    DROP CONSTRAINT IF EXISTS memory_relations_target_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
    ADD CONSTRAINT memory_relations_source_id_fkey
        FOREIGN KEY (source_id) REFERENCES memories(id) ON DELETE CASCADE,
    ADD CONSTRAINT memory_relations_target_id_fkey
        FOREIGN KEY (target_id) REFERENCES memories(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_workspace_id_fkey,
    DROP CONSTRAINT IF EXISTS memory_relations_source_id_fkey,
    DROP CONSTRAINT IF EXISTS memory_relations_target_id_fkey;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id),
    ADD CONSTRAINT memory_relations_source_id_fkey
        FOREIGN KEY (source_id) REFERENCES memories(id),
    ADD CONSTRAINT memory_relations_target_id_fkey
        FOREIGN KEY (target_id) REFERENCES memories(id);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_memories_created_by_user_id;
DROP INDEX IF EXISTS idx_memories_source_file_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE embeddings_text
    DROP CONSTRAINT IF EXISTS uq_embeddings_text_file_chunk;
-- +goose StatementEnd
