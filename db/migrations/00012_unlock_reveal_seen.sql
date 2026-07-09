-- +goose Up
ALTER TABLE capsule_members
    ADD COLUMN unlock_reveal_seen_at timestamptz;

-- +goose Down
ALTER TABLE capsule_members
    DROP COLUMN unlock_reveal_seen_at;
