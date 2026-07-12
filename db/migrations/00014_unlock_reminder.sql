-- +goose Up
ALTER TABLE capsules
    ADD COLUMN unlock_reminder_sent_at timestamptz;

-- +goose Down
ALTER TABLE capsules DROP COLUMN unlock_reminder_sent_at;
