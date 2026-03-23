-- +goose Up
-- +goose StatementBegin
-- Add chat_enabled field to live_streams table
-- Migration: 054
-- Description: Add support for enabling/disabling chat on live streams

-- Add chat_enabled column with default true (existing streams have chat enabled by default)
ALTER TABLE live_streams
ADD COLUMN IF NOT EXISTS chat_enabled BOOLEAN NOT NULL DEFAULT true;

-- Add index for queries that filter by chat_enabled status
CREATE INDEX IF NOT EXISTS idx_live_streams_chat_enabled
ON live_streams(chat_enabled)
WHERE chat_enabled = true;

-- Add comment for documentation
COMMENT ON COLUMN live_streams.chat_enabled IS 'Whether chat is enabled for this live stream';
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
