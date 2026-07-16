-- +goose Up
CREATE TABLE analytics_events (
    id           UUID PRIMARY KEY,
    user_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    event_name   TEXT NOT NULL,
    properties   JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at  TIMESTAMPTZ NOT NULL,
    source       TEXT NOT NULL CHECK (source IN ('server', 'client')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX analytics_events_name_occurred_idx
    ON analytics_events (event_name, occurred_at DESC);

CREATE INDEX analytics_events_user_occurred_idx
    ON analytics_events (user_id, occurred_at DESC)
    WHERE user_id IS NOT NULL;

CREATE INDEX analytics_events_props_capsule_idx
    ON analytics_events ((properties->>'capsule_id'), event_name, occurred_at DESC)
    WHERE properties ? 'capsule_id';

-- +goose Down
DROP TABLE IF EXISTS analytics_events;
