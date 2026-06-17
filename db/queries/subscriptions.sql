-- name: InsertSubscriptionEvent :one
INSERT INTO subscription_events (user_id, revenuecat_event_id, event_type, plan, raw)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (revenuecat_event_id) DO NOTHING
RETURNING *;

-- name: GetSubscriptionEventByRCID :one
SELECT * FROM subscription_events WHERE revenuecat_event_id = $1;

-- name: GetLatestSubscriptionEventByUserID :one
SELECT *
FROM subscription_events
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT 1;
