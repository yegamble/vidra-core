-- +goose Up
ALTER TABLE remote_runners
    ADD COLUMN IF NOT EXISTS runner_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ip_address TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS capabilities JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE remote_runners
    DROP COLUMN IF EXISTS capabilities,
    DROP COLUMN IF EXISTS ip_address,
    DROP COLUMN IF EXISTS runner_version;
