-- name: ListWidgetAlbumPhotos :many
SELECT DISTINCT ON (a.id)
       m.id AS memory_id,
       a.id AS album_id,
       a.name AS album_name,
       m.caption,
       (
         SELECT c.body
         FROM comments c
         WHERE c.memory_id = m.id
         ORDER BY c.created_at DESC
         LIMIT 1
       ) AS latest_comment,
       med.thumb_bucket_key,
       med.bucket_key AS media_key,
       med.kind AS media_kind
FROM albums a
JOIN album_members am ON am.album_id = a.id AND am.user_id = $1
JOIN memories m ON m.container_id = a.id AND m.container_type = 'album'
JOIN media med ON med.id = m.media_id
WHERE am.invite_status = 'accepted'
  AND am.left_at IS NULL
  AND a.state = 'active'
  AND m.deleted_at IS NULL
  AND med.status = 'ready'
ORDER BY a.id, m.created_at DESC;

-- name: ListWidgetCapsuleCountdowns :many
SELECT c.id AS capsule_id, c.name AS capsule_name, c.unlock_at
FROM capsules c
JOIN capsule_members cm ON cm.capsule_id = c.id AND cm.user_id = $1
WHERE cm.invite_status = 'accepted'
  AND c.state IN ('pending', 'active', 'frozen')
ORDER BY c.unlock_at;

-- name: ListWidgetUnlockedCapsuleMemories :many
SELECT
       m.id AS memory_id,
       c.id AS capsule_id,
       c.name AS capsule_name,
       m.caption,
       (
         SELECT cm.body
         FROM comments cm
         WHERE cm.memory_id = m.id
         ORDER BY cm.created_at DESC
         LIMIT 1
       ) AS latest_comment,
       med.thumb_bucket_key,
       med.bucket_key AS media_key,
       med.kind AS media_kind,
       m.created_at
FROM users u
JOIN capsule_members cm ON cm.user_id = u.id
JOIN capsules c ON c.id = cm.capsule_id
JOIN memories m ON m.container_id = c.id AND m.container_type = 'capsule'
JOIN media med ON med.id = m.media_id
WHERE u.id = $1
  AND cm.invite_status = 'accepted'
  AND cm.view_blocked = false
  AND c.state IN ('unlocked', 'archived')
  AND (c.viewable_until IS NULL OR c.viewable_until > now())
  AND m.deleted_at IS NULL
  AND med.status = 'ready'
ORDER BY m.created_at DESC
LIMIT 200;
