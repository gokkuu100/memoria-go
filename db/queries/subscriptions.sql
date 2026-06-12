-- name: InsertSubscriptionEvent :one
INSERT INTO subscription_events (user_id, revenuecat_event_id, event_type, plan, raw)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (revenuecat_event_id) DO NOTHING
RETURNING *;

-- name: GetSubscriptionEventByRCID :one
SELECT * FROM subscription_events WHERE revenuecat_event_id = $1;
