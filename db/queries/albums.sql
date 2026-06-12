-- name: CreateAlbum :one
INSERT INTO albums (creator_id, name, cover_style, state, invite_expires_at)
VALUES ($1, $2, $3, 'pending', $4)
RETURNING *;

-- name: GetAlbumByID :one
SELECT * FROM albums WHERE id = $1;

-- name: AddAlbumMember :exec
INSERT INTO album_members (album_id, user_id, invite_status, accepted_at)
VALUES ($1, $2, $3, $4);

-- name: GetAlbumMember :one
SELECT * FROM album_members WHERE album_id = $1 AND user_id = $2;

-- name: AcceptAlbumInvite :one
UPDATE album_members
SET invite_status = 'accepted', accepted_at = now()
WHERE album_id = $1 AND user_id = $2 AND invite_status = 'pending'
RETURNING *;

-- name: DeclineAlbumInvite :one
UPDATE album_members
SET invite_status = 'declined'
WHERE album_id = $1 AND user_id = $2 AND invite_status = 'pending'
RETURNING *;

-- name: ActivateAlbum :one
UPDATE albums
SET state = 'active', activated_at = now()
WHERE id = $1 AND state = 'pending'
RETURNING *;

-- name: MarkAlbumDeleted :one
UPDATE albums SET state = 'deleted' WHERE id = $1 RETURNING *;

-- name: ArchiveAlbum :one
UPDATE albums
SET state = 'archived', archive_until = $2
WHERE id = $1 AND state = 'active'
RETURNING *;

-- name: CountActiveAlbumsForUser :one
SELECT COUNT(*)::bigint AS count
FROM album_members am
JOIN albums a ON a.id = am.album_id
WHERE am.user_id = $1
  AND am.invite_status = 'accepted'
  AND am.left_at IS NULL
  AND a.state = 'active';

-- name: CountAlbumMembers :one
SELECT COUNT(*)::bigint AS count
FROM album_members
WHERE album_id = $1 AND invite_status = 'accepted' AND left_at IS NULL;

-- name: ListAlbumMembers :many
SELECT am.*, u.username, u.display_name, u.avatar_media_id
FROM album_members am
JOIN users u ON u.id = am.user_id
WHERE am.album_id = $1 AND u.deleted_at IS NULL
ORDER BY am.accepted_at NULLS LAST, am.user_id;

-- name: ListAlbumsForUser :many
SELECT a.*
FROM albums a
JOIN album_members am ON am.album_id = a.id
WHERE am.user_id = $1
  AND am.invite_status = 'accepted'
  AND am.left_at IS NULL
  AND a.state = $2
ORDER BY COALESCE(a.activated_at, a.created_at) DESC;

-- name: MarkMemberLeft :one
UPDATE album_members
SET left_at = now()
WHERE album_id = $1 AND user_id = $2 AND left_at IS NULL
RETURNING *;

-- name: CountActiveMembers :one
SELECT COUNT(*)::bigint AS count
FROM album_members
WHERE album_id = $1 AND invite_status = 'accepted' AND left_at IS NULL;

-- name: ListPendingExpiredAlbums :many
SELECT * FROM albums
WHERE state = 'pending' AND invite_expires_at <= now()
ORDER BY invite_expires_at
LIMIT 100;

-- name: ListActiveAlbumsWithCreatorPlan :many
SELECT a.*, u.plan AS creator_plan
FROM albums a
JOIN users u ON u.id = a.creator_id
WHERE a.state = 'active'
  AND a.activated_at IS NOT NULL
ORDER BY a.activated_at
LIMIT 100;

-- name: ListArchivedAlbumsPastRetention :many
SELECT * FROM albums
WHERE state = 'archived'
  AND archive_until IS NOT NULL
  AND archive_until <= now()
ORDER BY archive_until
LIMIT 100;

-- name: HardDeleteAlbum :exec
DELETE FROM albums WHERE id = $1;
