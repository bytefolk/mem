-- +goose Up
-- Agent-namespace recall filters producer_agent after workspace/path/time/kind.

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_memories_workspace_producer
    ON memories (workspace_id, producer_agent);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_memories_workspace_producer;
-- +goose StatementEnd
