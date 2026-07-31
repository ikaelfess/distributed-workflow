-- +goose Up
CREATE TABLE authentication_sessions (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users (id),
    csrf_secret_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    client_metadata TEXT NOT NULL DEFAULT '',
    ip INET,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX authentication_sessions_user_active_idx
    ON authentication_sessions (user_id, created_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE access_tokens (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    session_id UUID NOT NULL REFERENCES authentication_sessions (id),
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT access_tokens_token_hash_unique UNIQUE (token_hash)
);

CREATE INDEX access_tokens_lookup_idx
    ON access_tokens (token_hash)
    WHERE revoked_at IS NULL;

CREATE TABLE refresh_tokens (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    session_id UUID NOT NULL REFERENCES authentication_sessions (id),
    family_id UUID NOT NULL,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    rotated_from_id UUID REFERENCES refresh_tokens (id),
    reused_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT refresh_tokens_token_hash_unique UNIQUE (token_hash)
);

CREATE INDEX refresh_tokens_lookup_idx
    ON refresh_tokens (token_hash)
    WHERE revoked_at IS NULL;

CREATE INDEX refresh_tokens_family_idx
    ON refresh_tokens (family_id);

-- +goose Down
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS access_tokens;
DROP TABLE IF EXISTS authentication_sessions;
