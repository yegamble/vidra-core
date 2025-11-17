-- +goose Up
-- +goose StatementBegin
-- Add webp_ipfs_cid column to user_avatars table for WebP variant storage
ALTER TABLE user_avatars
    ADD COLUMN webp_ipfs_cid TEXT;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
