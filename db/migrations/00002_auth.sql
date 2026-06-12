-- +goose Up
CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           citext      NOT NULL,
    username        citext      NOT NULL,
    display_name    text        NOT NULL,
    password_hash   text        NOT NULL,
    avatar_media_id uuid,
    timezone        text        NOT NULL DEFAULT 'UTC',
    plan            text        NOT NULL DEFAULT 'spark',
    plan_expires_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

-- Partial unique indexes: email/username uniqueness applies to live accounts.
-- Deleted accounts free their email; usernames stay blocked via retired_usernames.
CREATE UNIQUE INDEX users_email_live_idx ON users (email) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX users_username_live_idx ON users (username) WHERE deleted_at IS NULL;

-- Usernames are retired permanently on account deletion and can never be reused.
CREATE TABLE retired_usernames (
    username   citext PRIMARY KEY,
    retired_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE otp_codes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email       citext      NOT NULL,
    code_hash   text        NOT NULL,
    purpose     text        NOT NULL CHECK (purpose IN ('signup', 'password_reset')),
    attempts    int         NOT NULL DEFAULT 0,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX otp_codes_email_purpose_idx ON otp_codes (email, purpose, created_at DESC);

CREATE TABLE sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_token_hash text        NOT NULL UNIQUE,
    device_name        text        NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE push_tokens (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expo_token text        NOT NULL,
    platform   text        NOT NULL CHECK (platform IN ('ios', 'android')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, expo_token)
);

-- +goose Down
DROP TABLE push_tokens;
DROP TABLE sessions;
DROP TABLE otp_codes;
DROP TABLE retired_usernames;
DROP TABLE users;
