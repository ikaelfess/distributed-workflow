-- +goose Up
CREATE TABLE challenges (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users (id),
    challenge_type TEXT NOT NULL,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    superseded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT challenges_type_valid CHECK (
        challenge_type IN ('verification', 'password_reset', 'email_change')
    ),
    CONSTRAINT challenges_token_hash_unique UNIQUE (token_hash),
    CONSTRAINT challenges_state_valid CHECK (
        NOT (consumed_at IS NOT NULL AND superseded_at IS NOT NULL)
    )
);

CREATE INDEX challenges_active_lookup_idx
    ON challenges (user_id, challenge_type, created_at DESC)
    WHERE consumed_at IS NULL AND superseded_at IS NULL;

CREATE TABLE audit_events (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    event_type TEXT NOT NULL,
    actor_user_id UUID,
    target_user_id UUID,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    retention_class TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT audit_events_retention_valid CHECK (
        retention_class IN ('routine', 'sensitive')
    )
);

CREATE INDEX audit_events_target_created_idx
    ON audit_events (target_user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS challenges;
