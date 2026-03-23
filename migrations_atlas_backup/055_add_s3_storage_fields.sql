-- 055_add_s3_storage_fields.sql
-- Add S3 storage tracking fields to videos table

ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS s3_urls JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS storage_tier VARCHAR(10) DEFAULT 'hot' CHECK (storage_tier IN ('hot', 'warm', 'cold')),
    ADD COLUMN IF NOT EXISTS s3_migrated_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS local_deleted BOOLEAN NOT NULL DEFAULT false;

-- Add index for querying videos by storage tier
CREATE INDEX IF NOT EXISTS idx_videos_storage_tier ON videos(storage_tier);

-- Add index for querying videos that need migration
CREATE INDEX IF NOT EXISTS idx_videos_s3_migration ON videos(storage_tier, s3_migrated_at) WHERE s3_migrated_at IS NULL;

-- Add index for finding videos with local files deleted
CREATE INDEX IF NOT EXISTS idx_videos_local_deleted ON videos(local_deleted) WHERE local_deleted = true;

-- Comment on columns
COMMENT ON COLUMN videos.s3_urls IS 'JSONB mapping of variant names to S3 URLs (e.g., {"720p": "https://...", "1080p": "https://..."})';
COMMENT ON COLUMN videos.storage_tier IS 'Storage tier: hot (local), warm (IPFS), cold (S3)';
COMMENT ON COLUMN videos.s3_migrated_at IS 'Timestamp when video was migrated to S3';
COMMENT ON COLUMN videos.local_deleted IS 'Whether local files have been deleted after S3 migration';
