-- +goose Up
-- +goose StatementBegin
-- Add support for federated remote videos
-- This allows Vidra Core to store references to videos from other PeerTube/ActivityPub instances

-- Add columns to videos table for remote video support
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS is_remote BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS remote_uri TEXT,
    ADD COLUMN IF NOT EXISTS remote_actor_uri TEXT,
    ADD COLUMN IF NOT EXISTS remote_video_url TEXT,
    ADD COLUMN IF NOT EXISTS remote_instance_domain TEXT,
    ADD COLUMN IF NOT EXISTS remote_thumbnail_url TEXT,
    ADD COLUMN IF NOT EXISTS remote_last_synced_at TIMESTAMP WITH TIME ZONE;

-- Add unique constraint for remote URIs (prevent duplicates)
ALTER TABLE videos
    ADD CONSTRAINT unique_remote_uri UNIQUE NULLS NOT DISTINCT (remote_uri);

-- Add index for remote videos
CREATE INDEX IF NOT EXISTS idx_videos_is_remote ON videos(is_remote);
CREATE INDEX IF NOT EXISTS idx_videos_remote_uri ON videos(remote_uri);
CREATE INDEX IF NOT EXISTS idx_videos_remote_instance_domain ON videos(remote_instance_domain);
CREATE INDEX IF NOT EXISTS idx_videos_remote_actor_uri ON videos(remote_actor_uri);

-- Add check constraint: remote videos must have remote_uri
ALTER TABLE videos
    ADD CONSTRAINT chk_remote_video_has_uri
    CHECK (is_remote = FALSE OR (is_remote = TRUE AND remote_uri IS NOT NULL));

-- Make original_cid nullable for remote videos (they don't have local files)
ALTER TABLE videos
    ALTER COLUMN original_cid DROP NOT NULL;

-- Make thumbnail_id nullable for remote videos (may not have local thumbnail)
ALTER TABLE videos
    ALTER COLUMN thumbnail_id DROP NOT NULL;

-- Add comment for documentation
COMMENT ON COLUMN videos.is_remote IS 'True if video is from a remote ActivityPub instance (federated video)';
COMMENT ON COLUMN videos.remote_uri IS 'ActivityPub URI of the remote video object';
COMMENT ON COLUMN videos.remote_actor_uri IS 'ActivityPub URI of the actor who created this video';
COMMENT ON COLUMN videos.remote_video_url IS 'URL to stream the video from the remote instance';
COMMENT ON COLUMN videos.remote_instance_domain IS 'Domain of the remote ActivityPub instance';
COMMENT ON COLUMN videos.remote_thumbnail_url IS 'URL of the video thumbnail on the remote instance';
COMMENT ON COLUMN videos.remote_last_synced_at IS 'Last time metadata was synced from remote instance';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Rollback remote video support

ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS chk_remote_video_has_uri,
    DROP CONSTRAINT IF EXISTS unique_remote_uri;

DROP INDEX IF EXISTS idx_videos_is_remote;
DROP INDEX IF EXISTS idx_videos_remote_uri;
DROP INDEX IF EXISTS idx_videos_remote_instance_domain;
DROP INDEX IF EXISTS idx_videos_remote_actor_uri;

ALTER TABLE videos
    DROP COLUMN IF EXISTS is_remote,
    DROP COLUMN IF EXISTS remote_uri,
    DROP COLUMN IF EXISTS remote_actor_uri,
    DROP COLUMN IF EXISTS remote_video_url,
    DROP COLUMN IF EXISTS remote_instance_domain,
    DROP COLUMN IF EXISTS remote_thumbnail_url,
    DROP COLUMN IF EXISTS remote_last_synced_at;

-- Note: original_cid and thumbnail_id nullability is not reversed
-- to avoid breaking existing data

-- +goose StatementEnd
