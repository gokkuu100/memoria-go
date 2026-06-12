-- +goose Up
CREATE TABLE friendships (
    user_a       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_b       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status       text        NOT NULL CHECK (status IN ('pending', 'accepted')),
    requested_by uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b)
);

CREATE TABLE blocks (
    blocker_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    blocked_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CHECK (blocker_id <> blocked_id)
);
CREATE INDEX blocks_blocked_id_idx ON blocks (blocked_id);

CREATE TABLE invite_links (
    token      text PRIMARY KEY,
    user_id    uuid        NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE invite_links;
DROP TABLE blocks;
DROP TABLE friendships;
