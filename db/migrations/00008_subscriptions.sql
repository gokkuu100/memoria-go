-- +goose Up
CREATE TABLE subscription_events (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              uuid        NOT NULL REFERENCES users (id),
    revenuecat_event_id  text        NOT NULL UNIQUE,
    event_type           text        NOT NULL,
    plan                 text        NOT NULL,
    raw                  jsonb       NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX subscription_events_user_id_idx ON subscription_events (user_id);

-- +goose Down
DROP TABLE IF EXISTS subscription_events;
