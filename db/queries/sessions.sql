-- name: CreateSession :one
INSERT INTO sessions (user_id, refresh_token_hash, device_name, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions WHERE refresh_token_hash = $1;

-- name: RotateSession :exec
UPDATE sessions
SET refresh_token_hash = $2, expires_at = $3
WHERE id = $1;

-- name: DeleteSessionByTokenHash :exec
DELETE FROM sessions WHERE refresh_token_hash = $1;

-- name: DeleteSessionsForUser :exec
DELETE FROM sessions WHERE user_id = $1;
