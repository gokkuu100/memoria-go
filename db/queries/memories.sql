-- name: CreateMemory :one
INSERT INTO memories (container_type, container_id, author_id, media_id, voice_media_id, caption, captured_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetMemoryIdempotency :one
SELECT user_id, idempotency_key, capsule_id, memory_id, created_at
FROM memory_idempotency
WHERE user_id = $1 AND idempotency_key = $2;

-- name: CreateMemoryIdempotency :one
INSERT INTO memory_idempotency (user_id, idempotency_key, capsule_id, memory_id)
VALUES ($1, $2, $3, $4)
RETURNING user_id, idempotency_key, capsule_id, memory_id, created_at;

-- name: GetMemoryByID :one
SELECT * FROM memories WHERE id = $1 AND deleted_at IS NULL;

-- name: CountAlbumMemories :one
SELECT COUNT(*)::bigint AS count
FROM memories
WHERE container_type = 'album'
  AND container_id = $1
  AND deleted_at IS NULL;

-- name: CountMemoriesByAuthorInAlbum :one
SELECT COUNT(*)::bigint AS count
FROM memories
WHERE container_type = 'album'
  AND container_id = $1
  AND author_id = $2
  AND deleted_at IS NULL;

-- name: ListAlbumMemories :many
SELECT m.*,
       (SELECT COUNT(*)::bigint FROM reactions r WHERE r.memory_id = m.id) AS reaction_count,
       (SELECT COUNT(*)::bigint FROM comments c WHERE c.memory_id = m.id) AS comment_count,
       COALESCE(
         (
           SELECT array_agg(emoji)
           FROM (
             SELECT r.emoji
             FROM reactions r
             WHERE r.memory_id = m.id
             ORDER BY r.created_at DESC
             LIMIT 4
           ) recent
         ),
         ARRAY[]::text[]
       ) AS reaction_emojis
FROM memories m
WHERE m.container_type = 'album'
  AND m.container_id = $1
  AND m.deleted_at IS NULL
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (m.created_at, m.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('page_limit');

-- name: ListMemoriesByAuthorInAlbum :many
SELECT m.*, med.bucket_key AS media_key, vm.bucket_key AS voice_key
FROM memories m
JOIN media med ON med.id = m.media_id
LEFT JOIN media vm ON vm.id = m.voice_media_id
WHERE m.container_type = 'album'
  AND m.container_id = $1
  AND m.author_id = $2
  AND m.deleted_at IS NULL;

-- name: SoftDeleteMemory :exec
UPDATE memories SET deleted_at = now() WHERE id = $1;

-- name: HardDeleteMemory :exec
DELETE FROM memories WHERE id = $1;

-- name: ListAlbumMemoryMediaKeys :many
SELECT m.id, med.bucket_key AS media_key, vm.bucket_key AS voice_key
FROM memories m
JOIN media med ON med.id = m.media_id
LEFT JOIN media vm ON vm.id = m.voice_media_id
WHERE m.container_type = 'album'
  AND m.container_id = $1
  AND m.deleted_at IS NULL;

-- name: ListCapsuleMemoryMediaKeys :many
SELECT m.id, med.bucket_key AS media_key, vm.bucket_key AS voice_key
FROM memories m
JOIN media med ON med.id = m.media_id
LEFT JOIN media vm ON vm.id = m.voice_media_id
WHERE m.container_type = 'capsule'
  AND m.container_id = $1
  AND m.deleted_at IS NULL;

-- name: UpsertReaction :one
INSERT INTO reactions (memory_id, user_id, emoji)
VALUES ($1, $2, $3)
ON CONFLICT (memory_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, created_at = now()
RETURNING *;

-- name: ListReactions :many
SELECT r.emoji, r.created_at, u.id AS user_id, u.username, u.display_name
FROM reactions r
JOIN users u ON u.id = r.user_id
WHERE r.memory_id = $1
ORDER BY r.created_at DESC;

-- name: DeleteReaction :execrows
DELETE FROM reactions WHERE memory_id = $1 AND user_id = $2;

-- name: CreateComment :one
INSERT INTO comments (memory_id, author_id, body)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetCommentByID :one
SELECT * FROM comments WHERE id = $1;

-- name: ListComments :many
SELECT c.*, u.username, u.display_name
FROM comments c
JOIN users u ON u.id = c.author_id
WHERE c.memory_id = $1
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (c.created_at, c.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('page_limit');

-- name: DeleteComment :execrows
DELETE FROM comments WHERE id = $1 AND author_id = $2 AND memory_id = $3;

-- name: ListCommentAuthorsExcept :many
SELECT DISTINCT author_id FROM comments
WHERE memory_id = $1 AND author_id <> $2;

-- name: ListTimelineAlbumMemories :many
SELECT m.id, m.container_id AS album_id, m.created_at,
       med.bucket_key AS media_key, a.name AS album_name
FROM memories m
JOIN media med ON med.id = m.media_id
JOIN albums a ON a.id = m.container_id
JOIN album_members am ON am.album_id = a.id AND am.user_id = $1
WHERE m.container_type = 'album'
  AND m.deleted_at IS NULL
  AND am.invite_status = 'accepted'
  AND am.left_at IS NULL
  AND a.state IN ('active', 'archived')
  AND m.created_at >= $2
  AND m.created_at < $3
ORDER BY m.created_at;
