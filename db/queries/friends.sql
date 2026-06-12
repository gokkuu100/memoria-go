-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1 AND deleted_at IS NULL;

-- name: AreFriends :one
SELECT EXISTS (
    SELECT 1 FROM friendships
    WHERE user_a = LEAST(@user_a::uuid, @user_b::uuid)
      AND user_b = GREATEST(@user_a::uuid, @user_b::uuid)
      AND status = 'accepted'
) AS friends;

-- name: IsBlockedEitherDirection :one
SELECT EXISTS (
    SELECT 1 FROM blocks
    WHERE (blocker_id = $1 AND blocked_id = $2)
       OR (blocker_id = $2 AND blocked_id = $1)
) AS blocked;

-- name: GetFriendship :one
SELECT * FROM friendships
WHERE user_a = LEAST(@user_a::uuid, @user_b::uuid)
  AND user_b = GREATEST(@user_a::uuid, @user_b::uuid);

-- name: CreateFriendship :one
INSERT INTO friendships (user_a, user_b, status, requested_by)
VALUES (
    LEAST(@user_a::uuid, @user_b::uuid),
    GREATEST(@user_a::uuid, @user_b::uuid),
    'pending',
    @requested_by
)
RETURNING *;

-- name: AcceptFriendship :one
UPDATE friendships
SET status = 'accepted'
WHERE user_a = LEAST(@recipient::uuid, @requester::uuid)
  AND user_b = GREATEST(@recipient::uuid, @requester::uuid)
  AND status = 'pending'
  AND requested_by = @requester
RETURNING *;

-- name: DeleteFriendship :exec
DELETE FROM friendships
WHERE user_a = LEAST(@user_a::uuid, @user_b::uuid)
  AND user_b = GREATEST(@user_a::uuid, @user_b::uuid);

-- name: DeleteOutgoingFriendRequest :execrows
DELETE FROM friendships
WHERE user_a = LEAST(@requester::uuid, @target::uuid)
  AND user_b = GREATEST(@requester::uuid, @target::uuid)
  AND status = 'pending'
  AND requested_by = @requester;

-- name: DeleteIncomingFriendRequest :execrows
DELETE FROM friendships
WHERE user_a = LEAST(@recipient::uuid, @requester::uuid)
  AND user_b = GREATEST(@recipient::uuid, @requester::uuid)
  AND status = 'pending'
  AND requested_by = @requester;

-- name: DeleteAcceptedFriendship :execrows
DELETE FROM friendships
WHERE user_a = LEAST(@user_a::uuid, @user_b::uuid)
  AND user_b = GREATEST(@user_a::uuid, @user_b::uuid)
  AND status = 'accepted';

-- name: ListAcceptedFriends :many
SELECT u.*
FROM friendships f
JOIN users u ON u.id = CASE WHEN f.user_a = $1 THEN f.user_b ELSE f.user_a END
WHERE (f.user_a = $1 OR f.user_b = $1)
  AND f.status = 'accepted'
  AND u.deleted_at IS NULL
ORDER BY u.display_name, u.username;

-- name: SearchAcceptedFriends :many
SELECT u.*
FROM friendships f
JOIN users u ON u.id = CASE WHEN f.user_a = $1 THEN f.user_b ELSE f.user_a END
WHERE (f.user_a = $1 OR f.user_b = $1)
  AND f.status = 'accepted'
  AND u.deleted_at IS NULL
  AND (u.username ILIKE '%' || @query || '%' OR u.display_name ILIKE '%' || @query || '%')
ORDER BY u.display_name, u.username;

-- name: ListIncomingFriendRequests :many
SELECT u.*
FROM friendships f
JOIN users u ON u.id = f.requested_by
WHERE (f.user_a = $1 OR f.user_b = $1)
  AND f.status = 'pending'
  AND f.requested_by <> $1
  AND u.deleted_at IS NULL
ORDER BY f.created_at DESC;

-- name: ListOutgoingFriendRequests :many
SELECT u.*
FROM friendships f
JOIN users u ON u.id = CASE WHEN f.user_a = $1 THEN f.user_b ELSE f.user_a END
WHERE (f.user_a = $1 OR f.user_b = $1)
  AND f.status = 'pending'
  AND f.requested_by = $1
  AND u.deleted_at IS NULL
ORDER BY f.created_at DESC;

-- name: GetInviteLinkByUser :one
SELECT * FROM invite_links WHERE user_id = $1;

-- name: CreateInviteLink :one
INSERT INTO invite_links (token, user_id) VALUES ($1, $2) RETURNING *;

-- name: GetInviteLinkByToken :one
SELECT * FROM invite_links WHERE token = $1;

-- name: CreateBlock :exec
INSERT INTO blocks (blocker_id, blocked_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DeleteBlock :execrows
DELETE FROM blocks WHERE blocker_id = $1 AND blocked_id = $2;
