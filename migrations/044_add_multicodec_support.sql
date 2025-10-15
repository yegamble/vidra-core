-- Migration: Add multi-codec support for VP9 and AV1 encoding
-- This migration extends the videos and encoding_jobs tables to support multiple codecs

-- Add encoding_profile column to encoding_jobs table
ALTER TABLE encoding_jobs
ADD COLUMN IF NOT EXISTS encoding_profile TEXT DEFAULT 'h264';

COMMENT ON COLUMN encoding_jobs.encoding_profile IS 'Codec profile: h264, vp9, or av1';

-- Update videos table to support multi-codec outputs
-- The outputs JSONB field will now store codec variants:
-- Example structure: {"h264": {"720p": "path/to/720p.m3u8", "1080p": "..."}, "vp9": {"720p": "..."}}
COMMENT ON COLUMN videos.outputs IS 'Multi-codec output paths stored as {"codec": {"resolution": "path"}}';

-- Add index on encoding_profile for faster queries
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_profile
ON encoding_jobs(encoding_profile);

-- Create a new table to track codec-specific encoding jobs for the same video
CREATE TABLE IF NOT EXISTS video_codec_variants (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    codec TEXT NOT NULL, -- 'h264', 'vp9', 'av1'
    status TEXT NOT NULL DEFAULT 'pending', -- 'pending', 'encoding', 'completed', 'failed'
    encoding_job_id UUID REFERENCES encoding_jobs(id) ON DELETE SET NULL,
    output_paths JSONB DEFAULT '{}', -- {"720p": "path", "1080p": "path"}
    file_sizes JSONB DEFAULT '{}', -- {"720p": 123456, "1080p": 234567}
    encoding_time_seconds INTEGER,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(video_id, codec)
);

COMMENT ON TABLE video_codec_variants IS 'Tracks encoding status for each codec variant of a video';
COMMENT ON COLUMN video_codec_variants.codec IS 'Video codec: h264, vp9, or av1';
COMMENT ON COLUMN video_codec_variants.status IS 'Encoding status for this codec variant';
COMMENT ON COLUMN video_codec_variants.output_paths IS 'Resolution-specific output paths for this codec';
COMMENT ON COLUMN video_codec_variants.file_sizes IS 'File sizes per resolution in bytes';

-- Create indexes for efficient queries
CREATE INDEX idx_video_codec_variants_video_id ON video_codec_variants(video_id);
CREATE INDEX idx_video_codec_variants_codec ON video_codec_variants(codec);
CREATE INDEX idx_video_codec_variants_status ON video_codec_variants(status);
CREATE INDEX idx_video_codec_variants_job_id ON video_codec_variants(encoding_job_id);

-- Create trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_video_codec_variants_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_video_codec_variants_updated_at
    BEFORE UPDATE ON video_codec_variants
    FOR EACH ROW
    EXECUTE FUNCTION update_video_codec_variants_updated_at();

-- Migrate existing videos to have h264 codec variant entries
-- This ensures backward compatibility
INSERT INTO video_codec_variants (video_id, codec, status, output_paths, completed_at)
SELECT
    id,
    'h264' as codec,
    CASE
        WHEN processing_status = 'completed' THEN 'completed'
        WHEN processing_status = 'processing' THEN 'encoding'
        WHEN processing_status = 'failed' THEN 'failed'
        ELSE 'pending'
    END as status,
    COALESCE(outputs, '{}'::jsonb) as output_paths,
    updated_at as completed_at
FROM videos
WHERE processing_status IS NOT NULL
ON CONFLICT (video_id, codec) DO NOTHING;

-- Add helper function to get available codecs for a video
CREATE OR REPLACE FUNCTION get_video_codecs(p_video_id UUID)
RETURNS TABLE(codec TEXT, status TEXT, has_outputs BOOLEAN) AS $$
BEGIN
    RETURN QUERY
    SELECT
        vcv.codec,
        vcv.status,
        (vcv.output_paths IS NOT NULL AND jsonb_typeof(vcv.output_paths) = 'object' AND vcv.output_paths != '{}'::jsonb) as has_outputs
    FROM video_codec_variants vcv
    WHERE vcv.video_id = p_video_id
    ORDER BY
        CASE vcv.codec
            WHEN 'h264' THEN 1
            WHEN 'vp9' THEN 2
            WHEN 'av1' THEN 3
            ELSE 4
        END;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION get_video_codecs IS 'Returns available codec variants for a video';
