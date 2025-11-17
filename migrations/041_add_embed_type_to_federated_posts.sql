-- +goose Up
-- +goose StatementBegin
-- Add embed_type to federated_posts to track media kind (external/images/video/record)
ALTER TABLE federated_posts
    ADD COLUMN IF NOT EXISTS embed_type TEXT;

-- Helpful index for filtering by type in timelines (optional)
CREATE INDEX IF NOT EXISTS idx_fed_posts_embed_type ON federated_posts(embed_type);
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
