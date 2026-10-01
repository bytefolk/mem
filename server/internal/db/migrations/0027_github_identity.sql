-- +goose Up
-- Authentication policy and provider identities are not workspace assets and
-- must never be exported in a workspace bundle. Provider tokens are not stored.
-- +goose StatementBegin
CREATE TABLE external_identities (
    provider text NOT NULL CHECK (provider = 'github'),
    subject text NOT NULL CHECK (subject ~ '^[1-9][0-9]{0,19}$'),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_login text NOT NULL,
    provider_email text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject),
    UNIQUE (user_id, provider)
);
CREATE TABLE oauth_challenges (
    state_hash text PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    intent text NOT NULL CHECK (intent IN ('login', 'link')),
    user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    session_id uuid REFERENCES sessions(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    CHECK ((intent = 'login' AND user_id IS NULL AND session_id IS NULL) OR (intent = 'link' AND user_id IS NOT NULL AND session_id IS NOT NULL))
);
CREATE INDEX oauth_challenges_expiry ON oauth_challenges(expires_at);
-- +goose StatementEnd

-- +goose Down
-- A downgrade cannot silently orphan OAuth-only users. Remove/migrate their
-- credentials explicitly before attempting rollback.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE password_hash = '!github-only') THEN
        RAISE EXCEPTION 'cannot downgrade while GitHub-only users exist';
    END IF;
END $$;
DROP TABLE oauth_challenges;
DROP TABLE external_identities;
-- +goose StatementEnd
