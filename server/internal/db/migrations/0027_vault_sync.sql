-- +goose Up
CREATE TABLE vaults (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    title text NOT NULL CHECK (octet_length(title) BETWEEN 1 AND 256),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision BETWEEN 0 AND 9007199254740991),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, id)
);

CREATE TABLE vault_commits (
    workspace_id uuid NOT NULL,
    vault_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    title text NOT NULL,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, vault_id, revision),
    FOREIGN KEY (workspace_id, vault_id) REFERENCES vaults(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE vault_entry_revisions (
    workspace_id uuid NOT NULL,
    vault_id uuid NOT NULL,
    note_id uuid NOT NULL,
    revision bigint NOT NULL,
    path text NOT NULL CHECK (octet_length(path) BETWEEN 1 AND 512),
    content text NOT NULL CHECK (octet_length(content) <= 1048576),
    properties jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(properties) = 'object' AND octet_length(properties::text) <= 16384),
    deleted boolean NOT NULL DEFAULT false,
    CHECK (NOT deleted OR content = ''),
    PRIMARY KEY (workspace_id, vault_id, note_id, revision),
    FOREIGN KEY (workspace_id, vault_id, revision) REFERENCES vault_commits(workspace_id, vault_id, revision) ON DELETE CASCADE
);

CREATE TABLE vault_entry_heads (
    workspace_id uuid NOT NULL,
    vault_id uuid NOT NULL,
    note_id uuid NOT NULL,
    revision bigint NOT NULL,
    path text NOT NULL,
    deleted boolean NOT NULL,
    PRIMARY KEY (workspace_id, vault_id, note_id),
    FOREIGN KEY (workspace_id, vault_id, note_id, revision)
        REFERENCES vault_entry_revisions(workspace_id, vault_id, note_id, revision) ON DELETE CASCADE
);
-- Blob identity is never note identity. Separate empty notes keep separate IDs.
CREATE UNIQUE INDEX vault_entry_active_path ON vault_entry_heads(workspace_id, vault_id, lower(path)) WHERE NOT deleted;

-- Content revisions are append-only; head pointers are the mutable projection.
-- +goose StatementBegin
CREATE FUNCTION reject_vault_entry_revision_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Vault entry revisions are immutable' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER vault_entry_revisions_immutable BEFORE UPDATE ON vault_entry_revisions
    FOR EACH ROW EXECUTE FUNCTION reject_vault_entry_revision_update();

-- +goose Down
DROP TABLE vault_entry_heads;
DROP TABLE vault_entry_revisions;
DROP FUNCTION reject_vault_entry_revision_update();
DROP TABLE vault_commits;
DROP TABLE vaults;
