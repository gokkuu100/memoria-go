-- +goose Up
CREATE TABLE notification_prefs (
    user_id  uuid    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category text    NOT NULL,
    enabled  boolean NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, category)
);

CREATE TABLE notification_outbox (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category      text        NOT NULL,
    title         text        NOT NULL,
    body          text        NOT NULL,
    data          jsonb       NOT NULL DEFAULT '{}',
    batch_key     text,
    deliver_after timestamptz NOT NULL DEFAULT now(),
    sent_at       timestamptz,
    read_at       timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notification_outbox_pending_idx
    ON notification_outbox (deliver_after)
    WHERE sent_at IS NULL;

-- +goose Down
DROP TABLE notification_outbox;
DROP TABLE notification_prefs;
