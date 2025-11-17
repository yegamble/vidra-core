-- +goose Up
-- +goose StatementBegin
-- Create videos table with UUID ids and thumbnail_id
CREATE TABLE IF NOT EXISTS videos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thumbnail_id UUID NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    duration INTEGER NOT NULL DEFAULT 0,
    views BIGINT NOT NULL DEFAULT 0,
    privacy VARCHAR(20) NOT NULL CHECK (privacy IN ('public','unlisted','private')),
    status VARCHAR(20) NOT NULL CHECK (status IN ('uploading','queued','processing','completed','failed')),
    upload_date TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    original_cid TEXT,
    processed_cids JSONB NOT NULL DEFAULT '{}'::jsonb,
    thumbnail_cid TEXT,
    tags TEXT[] NOT NULL DEFAULT '{}',
    category VARCHAR(100),
    language VARCHAR(10),
    file_size BIGINT NOT NULL DEFAULT 0,
    mime_type VARCHAR(120),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_videos_user_id ON videos(user_id);
CREATE INDEX IF NOT EXISTS idx_videos_privacy ON videos(privacy);
CREATE INDEX IF NOT EXISTS idx_videos_status ON videos(status);
CREATE INDEX IF NOT EXISTS idx_videos_upload_date ON videos(upload_date);

-- Trigger to update updated_at
DROP TRIGGER IF EXISTS update_videos_updated_at ON videos;
CREATE TRIGGER update_videos_updated_at
    BEFORE UPDATE ON videos
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
