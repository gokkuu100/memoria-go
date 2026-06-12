-- +goose Up
-- Extensions used across the schema: pgcrypto for gen_random_uuid(),
-- citext for case-insensitive emails/usernames.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- +goose Down
DROP EXTENSION IF EXISTS citext;
DROP EXTENSION IF EXISTS pgcrypto;
