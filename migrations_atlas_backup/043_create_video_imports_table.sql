-- Migration: Create video imports table
-- This migration adds support for importing videos from external sources (YouTube, Vimeo, etc.)

-- Create enum type for import status
CREATE TYPE import_status AS ENUM ('pending', 'downloading', 'processing', 'completed', 'failed', 'cancelled');

-- Create video_imports table
CREATE TABLE video_imports (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id UUID REFERENCES channels(id) ON DELETE SET NULL,
    source_url TEXT NOT NULL,
    status import_status NOT NULL DEFAULT 'pending',
    video_id UUID REFERENCES videos(id) ON DELETE SET NULL,
    error_message TEXT,
    progress INTEGER DEFAULT 0 CHECK (progress >= 0 AND progress <= 100),
    metadata JSONB, -- Store yt-dlp metadata (title, description, duration, etc.)
    file_size_bytes BIGINT,
    downloaded_bytes BIGINT DEFAULT 0,
    target_privacy TEXT DEFAULT 'private', -- Privacy setting for imported video
    target_category TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT valid_completion CHECK (
        (status = 'completed' AND completed_at IS NOT NULL) OR
        (status != 'completed')
    ),
    CONSTRAINT valid_dates CHECK (
        (started_at IS NULL OR started_at >= created_at) AND
        (completed_at IS NULL OR (started_at IS NOT NULL AND completed_at >= started_at))
    )
);

-- Create indexes for common queries
CREATE INDEX idx_video_imports_user_id ON video_imports(user_id);
CREATE INDEX idx_video_imports_channel_id ON video_imports(channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX idx_video_imports_status ON video_imports(status);
CREATE INDEX idx_video_imports_created_at ON video_imports(created_at DESC);
CREATE INDEX idx_video_imports_video_id ON video_imports(video_id) WHERE video_id IS NOT NULL;
CREATE INDEX idx_video_imports_user_status ON video_imports(user_id, status);

-- Add trigger for updated_at timestamp
CREATE TRIGGER update_video_imports_updated_at
    BEFORE UPDATE ON video_imports
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Add comment for documentation
COMMENT ON TABLE video_imports IS 'Tracks video imports from external sources like YouTube, Vimeo, etc.';
COMMENT ON COLUMN video_imports.metadata IS 'JSON metadata from yt-dlp including original title, description, uploader, etc.';
COMMENT ON COLUMN video_imports.progress IS 'Download progress percentage (0-100)';
COMMENT ON COLUMN video_imports.target_privacy IS 'Privacy setting to apply to the imported video (public, unlisted, private)';
