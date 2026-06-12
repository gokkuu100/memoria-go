-- name: CreateUser :one
INSERT INTO users (email, username, display_name, password_hash, timezone)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;

-- name: EmailTaken :one
SELECT EXISTS (
    SELECT 1 FROM users WHERE email = $1 AND deleted_at IS NULL
) AS taken;

-- name: UsernameTaken :one
SELECT (
    EXISTS (SELECT 1 FROM users u WHERE u.username = $1 AND u.deleted_at IS NULL)
    OR EXISTS (SELECT 1 FROM retired_usernames r WHERE r.username = $1)
) AS taken;

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    timezone     = COALESCE(sqlc.narg('timezone'), timezone)
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: RetireUsername :exec
INSERT INTO retired_usernames (username)
VALUES ($1)
ON CONFLICT (username) DO NOTHING;

-- name: UpdateUserPlan :one
UPDATE users
SET plan = $2,
    plan_expires_at = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
