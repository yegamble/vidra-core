-- +goose Up
-- +goose StatementBegin
-- Fix unique_remote_uri constraint that incorrectly treats NULL = NULL.
-- UNIQUE NULLS NOT DISTINCT prevents more than one local video (where remote_uri IS NULL).
-- Replace with a partial unique index that only enforces uniqueness for non-NULL remote_uri values.

ALTER TABLE videos DROP CONSTRAINT IF EXISTS unique_remote_uri;

CREATE UNIQUE INDEX IF NOT EXISTS idx_videos_remote_uri_unique_nonnull
    ON videos(remote_uri) WHERE remote_uri IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_videos_remote_uri_unique_nonnull;

ALTER TABLE videos
    ADD CONSTRAINT unique_remote_uri UNIQUE NULLS NOT DISTINCT (remote_uri);

-- +goose StatementEnd
