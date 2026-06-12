-- +goose Up
CREATE TABLE media (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     uuid        NOT NULL REFERENCES users (id),
    kind         text        NOT NULL CHECK (kind IN ('photo', 'video', 'voice')),
    bucket_key   text        NOT NULL UNIQUE,
    content_type text        NOT NULL,
    duration_ms  int,
    width        int,
    height       int,
    byte_size    bigint,
    status       text        NOT NULL CHECK (status IN ('pending', 'ready')) DEFAULT 'pending',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX media_owner_idx ON media (owner_id);
-- Supports the orphan sweep: pending rows older than a cutoff.
CREATE INDEX media_status_created_idx ON media (status, created_at);

-- +goose Down
DROP TABLE media;
