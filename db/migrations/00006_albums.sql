-- +goose Up
CREATE TABLE albums (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id        uuid        NOT NULL REFERENCES users (id),
    name              text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    cover_style       text        NOT NULL CHECK (cover_style IN (
        'espresso', 'gold', 'cream', 'dot_grid', 'wave', 'gradient_sunset'
    )),
    state             text        NOT NULL CHECK (state IN ('pending', 'active', 'archived', 'deleted'))
        DEFAULT 'pending',
    activated_at      timestamptz,
    archive_until     timestamptz,
    invite_expires_at timestamptz NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX albums_creator_idx ON albums (creator_id);
CREATE INDEX albums_state_idx ON albums (state);
CREATE INDEX albums_invite_expires_idx ON albums (state, invite_expires_at)
    WHERE state = 'pending';

CREATE TABLE album_members (
    album_id      uuid        NOT NULL REFERENCES albums (id) ON DELETE CASCADE,
    user_id       uuid        NOT NULL REFERENCES users (id),
    invite_status text        NOT NULL CHECK (invite_status IN ('pending', 'accepted', 'declined')),
    accepted_at   timestamptz,
    left_at       timestamptz,
    PRIMARY KEY (album_id, user_id)
);

CREATE INDEX album_members_user_idx ON album_members (user_id);

CREATE TABLE memories (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    container_type  text        NOT NULL CHECK (container_type IN ('capsule', 'album')),
    container_id    uuid        NOT NULL,
    author_id       uuid        NOT NULL REFERENCES users (id),
    media_id        uuid        NOT NULL REFERENCES media (id),
    voice_media_id  uuid        REFERENCES media (id),
    caption         text        CHECK (caption IS NULL OR char_length(caption) <= 500),
    created_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE INDEX memories_container_idx ON memories (container_type, container_id, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX memories_author_idx ON memories (author_id);

CREATE TABLE reactions (
    memory_id  uuid        NOT NULL REFERENCES memories (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id),
    emoji      text        NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (memory_id, user_id)
);

CREATE TABLE comments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_id  uuid        NOT NULL REFERENCES memories (id) ON DELETE CASCADE,
    author_id  uuid        NOT NULL REFERENCES users (id),
    body       text        NOT NULL CHECK (char_length(body) BETWEEN 1 AND 1000),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX comments_memory_idx ON comments (memory_id, created_at);

-- +goose Down
DROP TABLE comments;
DROP TABLE reactions;
DROP TABLE memories;
DROP TABLE album_members;
DROP TABLE albums;
