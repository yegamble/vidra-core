-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS default_video_privacy VARCHAR(20) NOT NULL DEFAULT 'public'
    CHECK (default_video_privacy IN ('public', 'unlisted', 'private'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    DROP COLUMN IF EXISTS default_video_privacy;
-- +goose StatementEnd
