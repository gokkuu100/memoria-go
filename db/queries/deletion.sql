-- name: ListActiveAlbumMembershipsForUser :many
SELECT am.album_id, a.state
FROM album_members am
JOIN albums a ON a.id = am.album_id
WHERE am.user_id = $1
  AND am.invite_status = 'accepted'
  AND am.left_at IS NULL
  AND a.state IN ('active', 'archived', 'pending');

-- name: ListAuthoredMemoriesWithMedia :many
SELECT m.id, med.bucket_key AS media_key, vm.bucket_key AS voice_key
FROM memories m
JOIN media med ON med.id = m.media_id
LEFT JOIN media vm ON vm.id = m.voice_media_id
WHERE m.author_id = $1 AND m.deleted_at IS NULL;

-- name: ListOwnedMediaKeys :many
SELECT id, bucket_key FROM media WHERE owner_id = $1;

-- name: DeleteReactionsForUser :exec
DELETE FROM reactions WHERE user_id = $1;

-- name: DeleteCommentsForUser :exec
DELETE FROM comments WHERE author_id = $1;

-- name: DeleteFriendshipsForUser :exec
DELETE FROM friendships WHERE user_a = $1 OR user_b = $1;

-- name: DeleteBlocksForUser :exec
DELETE FROM blocks WHERE blocker_id = $1 OR blocked_id = $1;

-- name: DeleteInviteLinkForUser :exec
DELETE FROM invite_links WHERE user_id = $1;

-- name: DeleteCapsuleMembershipsForUser :exec
DELETE FROM capsule_members WHERE user_id = $1;

-- name: DeleteNotificationPrefsForUser :exec
DELETE FROM notification_prefs WHERE user_id = $1;

-- name: DeleteNotificationOutboxForUser :exec
DELETE FROM notification_outbox WHERE user_id = $1;

-- name: GetUserByIDIncludingDeleted :one
SELECT * FROM users WHERE id = $1;
