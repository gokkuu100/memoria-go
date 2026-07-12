-- +goose Up
ALTER TABLE memories
    ADD COLUMN captured_at timestamptz;

UPDATE memories SET captured_at = created_at WHERE captured_at IS NULL;

ALTER TABLE memories
    ALTER COLUMN captured_at SET NOT NULL;

CREATE INDEX memories_capsule_captured_idx
    ON memories (container_id, captured_at DESC)
    WHERE container_type = 'capsule' AND deleted_at IS NULL;

CREATE TABLE memory_idempotency (
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    idempotency_key uuid        NOT NULL,
    capsule_id      uuid        NOT NULL,
    memory_id       uuid        NOT NULL REFERENCES memories (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, idempotency_key)
);

CREATE INDEX memory_idempotency_memory_idx ON memory_idempotency (memory_id);

-- +goose Down
DROP TABLE memory_idempotency;
DROP INDEX IF EXISTS memories_capsule_captured_idx;
ALTER TABLE memories DROP COLUMN captured_at;
