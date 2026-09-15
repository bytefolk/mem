-- +goose Up
-- Data-plane hygiene: uniqueness, missing FK indexes, and cascade alignment.
-- See https://github.com/bytefolk/mem/issues/178

-- 1. embeddings_text: enforce one row per (file_id, chunk_index).
--    The write path (indexer.go) DELETEs by file_id then batch-inserts; this
--    constraint gives the invariant database-level teeth, matching the
--    per-file guarantee that embeddings_visual already has via its PK.
-- +goose StatementBegin
ALTER TABLE embeddings_text
    ADD CONSTRAINT uq_embeddings_text_file_chunk UNIQUE (file_id, chunk_index);
-- +goose StatementEnd

-- 2. memories: index the unindexed ON DELETE SET NULL foreign keys.
--    Without these, every file or user delete takes a RowExclusiveLock on
--    memories and performs a sequential scan to find rows to null out.
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_memories_source_file_id
    ON memories (source_file_id) WHERE source_file_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_memories_created_by_user_id
    ON memories (created_by_user_id) WHERE created_by_user_id IS NOT NULL;
-- +goose StatementEnd

-- 3. memory_relations: align FK referential actions with memories CASCADE.
--    memories.workspace_id is ON DELETE CASCADE, but memory_relations FKs
--    defaulted to NO ACTION, so a cascading workspace delete would fail on
--    any edge touching the deleted memories.
-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_workspace_id_fkey;
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_workspace_id_fkey
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_source_id_fkey;
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES memories(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_target_id_fkey;
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_target_id_fkey
    FOREIGN KEY (target_id) REFERENCES memories(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_target_id_fkey;
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_target_id_fkey
    FOREIGN KEY (target_id) REFERENCES memories(id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_source_id_fkey;
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES memories(id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE memory_relations
    DROP CONSTRAINT IF EXISTS memory_relations_workspace_id_fkey;
ALTER TABLE memory_relations
    ADD CONSTRAINT memory_relations_workspace_id_fkey
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_memories_created_by_user_id;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS idx_memories_source_file_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE embeddings_text
    DROP CONSTRAINT IF EXISTS uq_embeddings_text_file_chunk;
-- +goose StatementEnd
