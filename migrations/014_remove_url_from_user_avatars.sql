-- +goose Up
-- +goose StatementBegin
-- Remove url column from user_avatars now that avatar is tracked by file_id and ipfs_cid
ALTER TABLE user_avatars
    DROP COLUMN IF EXISTS url;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
