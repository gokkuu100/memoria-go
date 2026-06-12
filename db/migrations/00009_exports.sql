-- +goose Up
CREATE TABLE export_jobs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL REFERENCES users (id),
    kind         text        NOT NULL CHECK (kind IN ('zip', 'pdf')),
    status       text        NOT NULL CHECK (status IN ('pending', 'processing', 'ready', 'failed'))
        DEFAULT 'pending',
    result_key   text,
    error        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX export_jobs_user_id_idx ON export_jobs (user_id);
CREATE INDEX export_jobs_status_idx ON export_jobs (status) WHERE status IN ('pending', 'processing');

-- +goose Down
DROP TABLE IF EXISTS export_jobs;
