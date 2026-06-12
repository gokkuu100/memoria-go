-- name: CreateMedia :one
INSERT INTO media (owner_id, kind, bucket_key, content_type, duration_ms, byte_size)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetMediaByID :one
SELECT * FROM media WHERE id = $1;

-- name: MarkMediaReady :one
UPDATE media
SET status = 'ready', byte_size = $2, width = $3, height = $4, thumb_bucket_key = $5
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: DeleteMedia :exec
DELETE FROM media WHERE id = $1;

-- name: ListExpiredPendingMedia :many
SELECT * FROM media
WHERE status = 'pending' AND created_at < $1
LIMIT 500;

-- name: SetUserAvatar :exec
UPDATE users SET avatar_media_id = $2 WHERE id = $1 AND deleted_at IS NULL;
