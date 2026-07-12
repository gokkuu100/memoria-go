-- name: CreateOutbox :one
INSERT INTO notification_outbox (user_id, category, title, body, data, batch_key, deliver_after)
VALUES ($1, $2, $3, $4, $5, $6, COALESCE(sqlc.narg('deliver_after')::timestamptz, now()))
RETURNING *;

-- name: ListDueInstantOutbox :many
SELECT *
FROM notification_outbox
WHERE sent_at IS NULL
  AND batch_key IS NULL
  AND deliver_after <= now()
ORDER BY deliver_after, created_at
LIMIT $1;

-- name: MarkOutboxSent :exec
UPDATE notification_outbox
SET sent_at = now()
WHERE id = $1 AND sent_at IS NULL;

-- name: GetNotificationPrefs :many
SELECT category, enabled
FROM notification_prefs
WHERE user_id = $1;

-- name: UpsertNotificationPref :exec
INSERT INTO notification_prefs (user_id, category, enabled)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, category) DO UPDATE SET enabled = EXCLUDED.enabled;

-- name: ListNotificationsForUser :many
SELECT *
FROM notification_outbox
WHERE user_id = $1
  AND sent_at IS NOT NULL
  AND (
    sqlc.narg('cursor_sent_at')::timestamptz IS NULL
    OR sent_at < sqlc.narg('cursor_sent_at')::timestamptz
    OR (sent_at = sqlc.narg('cursor_sent_at')::timestamptz AND id < sqlc.narg('cursor_id')::uuid)
  )
ORDER BY sent_at DESC, id DESC
LIMIT sqlc.arg('page_limit');

-- name: CountUnreadNotifications :one
SELECT count(*)::bigint
FROM notification_outbox
WHERE user_id = $1
  AND sent_at IS NOT NULL
  AND read_at IS NULL;

-- name: DeleteNotificationForUser :execrows
DELETE FROM notification_outbox
WHERE id = $1
  AND user_id = $2
  AND sent_at IS NOT NULL;

-- name: MarkNotificationRead :execrows
UPDATE notification_outbox
SET read_at = now()
WHERE id = $1
  AND user_id = $2
  AND sent_at IS NOT NULL
  AND read_at IS NULL;

-- name: AdvanceUnlockNotificationsForUser :exec
UPDATE notification_outbox
SET deliver_after = now()
WHERE user_id = $1
  AND category = 'capsule_unlock'
  AND sent_at IS NULL
  AND deliver_after > now();

-- name: MarkAllNotificationsRead :exec
UPDATE notification_outbox
SET read_at = now()
WHERE user_id = $1
  AND sent_at IS NOT NULL
  AND read_at IS NULL;

-- name: ListDueBatchKeys :many
SELECT DISTINCT batch_key
FROM notification_outbox
WHERE sent_at IS NULL
  AND batch_key IS NOT NULL
  AND deliver_after <= now()
ORDER BY batch_key
LIMIT $1;

-- name: ListDueBatchKeysForUser :many
SELECT DISTINCT batch_key
FROM notification_outbox
WHERE user_id = $1
  AND sent_at IS NULL
  AND batch_key IS NOT NULL
  AND deliver_after <= now()
ORDER BY batch_key;

-- name: ListPendingBatchRows :many
SELECT *
FROM notification_outbox
WHERE batch_key = $1
  AND sent_at IS NULL
ORDER BY created_at;

-- name: GetPendingBatchMinDeliverAfter :one
SELECT min(deliver_after)::timestamptz AS deliver_after
FROM notification_outbox
WHERE batch_key = $1
  AND sent_at IS NULL;

-- name: GetLastBatchSentAt :one
SELECT max(sent_at)::timestamptz AS sent_at
FROM notification_outbox
WHERE batch_key = $1
  AND sent_at IS NOT NULL;

-- name: MarkOutboxSentMany :exec
UPDATE notification_outbox
SET sent_at = now()
WHERE id = ANY($1::uuid[])
  AND sent_at IS NULL;

-- name: UpdateOutboxContent :exec
UPDATE notification_outbox
SET title = $2, body = $3, data = $4
WHERE id = $1;

-- name: DeleteOutboxRows :exec
DELETE FROM notification_outbox
WHERE id = ANY($1::uuid[]);
