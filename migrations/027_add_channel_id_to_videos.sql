-- +goose Up
-- +goose StatementBegin
-- Add channel_id to videos table
-- First add as nullable, backfill, then make non-nullable

-- Step 1: Add nullable channel_id column
ALTER TABLE videos
ADD COLUMN IF NOT EXISTS channel_id UUID REFERENCES channels(id) ON DELETE CASCADE;

-- Step 2: Create index for performance
CREATE INDEX IF NOT EXISTS idx_videos_channel_id ON videos(channel_id);

-- Step 3: Backfill channel_id with default channel for each video's owner
-- Each user should have a default channel from the previous migration
UPDATE videos v
SET channel_id = (
    SELECT c.id
    FROM channels c
    WHERE c.account_id = v.user_id
    ORDER BY c.created_at ASC
    LIMIT 1
)
WHERE v.channel_id IS NULL;

-- Step 4: Make channel_id NOT NULL after backfill
-- Note: In production, this might need to be a separate migration
-- to allow for gradual rollout
ALTER TABLE videos
ALTER COLUMN channel_id SET NOT NULL;

-- Add comment
COMMENT ON COLUMN videos.channel_id IS 'Channel that owns this video (PeerTube model)';

-- Update trigger for videos_count on channels
CREATE OR REPLACE FUNCTION update_channel_video_count()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE channels SET videos_count = videos_count + 1
        WHERE id = NEW.channel_id;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE channels SET videos_count = videos_count - 1
        WHERE id = OLD.channel_id;
    ELSIF TG_OP = 'UPDATE' AND OLD.channel_id != NEW.channel_id THEN
        UPDATE channels SET videos_count = videos_count - 1
        WHERE id = OLD.channel_id;
        UPDATE channels SET videos_count = videos_count + 1
        WHERE id = NEW.channel_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_channel_video_count_trigger
AFTER INSERT OR DELETE OR UPDATE OF channel_id ON videos
FOR EACH ROW
EXECUTE FUNCTION update_channel_video_count();

-- Recalculate initial counts
UPDATE channels c
SET videos_count = (
    SELECT COUNT(*)
    FROM videos v
    WHERE v.channel_id = c.id
);
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
