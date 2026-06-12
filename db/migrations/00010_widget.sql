-- +goose Up
ALTER TABLE media ADD COLUMN thumb_bucket_key text;

ALTER TABLE push_tokens ADD COLUMN widget_push_at timestamptz;

-- +goose Down
ALTER TABLE push_tokens DROP COLUMN widget_push_at;
ALTER TABLE media DROP COLUMN thumb_bucket_key;
