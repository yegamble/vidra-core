-- Create captions table for video subtitle/caption tracks
CREATE TABLE IF NOT EXISTS captions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    language_code VARCHAR(10) NOT NULL, -- e.g., 'en', 'en-US', 'es', 'fr'
    label VARCHAR(100) NOT NULL, -- e.g., 'English', 'English (US)', 'Spanish'
    file_path TEXT, -- local file path for hot storage
    ipfs_cid TEXT, -- IPFS CID for distributed storage
    file_format VARCHAR(10) NOT NULL CHECK (file_format IN ('vtt', 'srt')), -- WebVTT or SubRip
    file_size_bytes BIGINT,
    is_auto_generated BOOLEAN NOT NULL DEFAULT FALSE, -- track if AI/auto generated
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(video_id, language_code) -- one caption per language per video
);

-- Create indexes for captions
CREATE INDEX idx_captions_video_id ON captions(video_id);
CREATE INDEX idx_captions_language_code ON captions(language_code);
CREATE INDEX idx_captions_created_at ON captions(created_at DESC);

-- Function to update caption updated_at timestamp
CREATE OR REPLACE FUNCTION update_caption_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for updating caption timestamp
CREATE TRIGGER trigger_captions_updated_at
    BEFORE UPDATE ON captions
    FOR EACH ROW
    EXECUTE FUNCTION update_caption_updated_at();

-- Add captions count to videos table for optimization
ALTER TABLE videos
ADD COLUMN IF NOT EXISTS captions_count INTEGER NOT NULL DEFAULT 0;

-- Function to update video caption counts
CREATE OR REPLACE FUNCTION update_video_caption_count()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE videos
        SET captions_count = captions_count + 1
        WHERE id = NEW.video_id;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE videos
        SET captions_count = captions_count - 1
        WHERE id = OLD.video_id;
    ELSIF TG_OP = 'UPDATE' AND OLD.video_id != NEW.video_id THEN
        -- Handle video_id change (unlikely but defensive)
        UPDATE videos
        SET captions_count = captions_count - 1
        WHERE id = OLD.video_id;

        UPDATE videos
        SET captions_count = captions_count + 1
        WHERE id = NEW.video_id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for updating video caption counts
CREATE TRIGGER trigger_update_video_caption_count
    AFTER INSERT OR UPDATE OR DELETE ON captions
    FOR EACH ROW
    EXECUTE FUNCTION update_video_caption_count();