-- +goose Up
CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    topic TEXT NOT NULL,
    event_key TEXT NOT NULL,
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until TIMESTAMPTZ,
    claim_token UUID,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    CONSTRAINT outbox_schema_version_positive CHECK (schema_version > 0),
    CONSTRAINT outbox_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT outbox_claim_consistent CHECK (
        (locked_until IS NULL AND claim_token IS NULL)
        OR (locked_until IS NOT NULL AND claim_token IS NOT NULL)
    )
);

CREATE INDEX outbox_events_pending_idx
    ON outbox_events (available_at, created_at)
    WHERE claim_token IS NULL;

CREATE INDEX outbox_events_expired_claim_idx
    ON outbox_events (locked_until)
    WHERE claim_token IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS outbox_events;
