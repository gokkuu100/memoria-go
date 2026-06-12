-- +goose Up
-- B11: indexes for notification feed/unread queries (list + count on sent rows).
CREATE INDEX notification_outbox_user_sent_idx
    ON notification_outbox (user_id, sent_at DESC)
    WHERE sent_at IS NOT NULL;

CREATE INDEX notification_outbox_user_unread_idx
    ON notification_outbox (user_id)
    WHERE sent_at IS NOT NULL AND read_at IS NULL;

-- Batch flush scans due batched rows by deliver_after.
CREATE INDEX notification_outbox_batch_pending_idx
    ON notification_outbox (deliver_after)
    WHERE sent_at IS NULL AND batch_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS notification_outbox_batch_pending_idx;
DROP INDEX IF EXISTS notification_outbox_user_unread_idx;
DROP INDEX IF EXISTS notification_outbox_user_sent_idx;
