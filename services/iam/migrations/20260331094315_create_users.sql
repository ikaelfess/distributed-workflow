-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unverified',
    access_level TEXT NOT NULL DEFAULT 'standard',
    email_verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_email_canonical CHECK (email = LOWER(BTRIM(email))),
    CONSTRAINT users_email_unique UNIQUE (email),
    CONSTRAINT users_status_valid CHECK (status IN ('unverified', 'active', 'suspended')),
    CONSTRAINT users_access_level_valid CHECK (access_level IN ('standard', 'administrator')),
    CONSTRAINT users_verification_state_valid CHECK (
        (status = 'unverified' AND email_verified_at IS NULL)
        OR status = 'suspended'
        OR (status = 'active' AND email_verified_at IS NOT NULL)
    )
);

-- +goose Down
DROP TABLE IF EXISTS users;
