-- name: UpsertPushToken :exec
INSERT INTO push_tokens (user_id, expo_token, platform)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, expo_token) DO UPDATE SET platform = $3, updated_at = now();

-- name: DeletePushTokensForUser :exec
DELETE FROM push_tokens WHERE user_id = $1;

-- name: ListPushTokensForUser :many
SELECT expo_token, platform
FROM push_tokens
WHERE user_id = $1;

-- name: DeletePushToken :exec
DELETE FROM push_tokens WHERE user_id = $1 AND expo_token = $2;

-- name: ListWidgetPushEligibleTokens :many
SELECT expo_token
FROM push_tokens
WHERE user_id = $1
  AND (widget_push_at IS NULL OR widget_push_at < now() - interval '15 minutes');

-- name: TouchWidgetPushAt :exec
UPDATE push_tokens
SET widget_push_at = now()
WHERE user_id = $1 AND expo_token = ANY($2::text[]);
