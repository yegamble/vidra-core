-- Create user_thumbnails table to store per-user thumbnail file UUID and IPFS CID
-- Keeps existing users.avatar for URL-based avatars while enabling file/CID-based thumbnails

BEGIN;

CREATE TABLE IF NOT EXISTS user_thumbnails (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    thumbnail_id UUID NOT NULL,
    thumbnail_cid TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Useful indexes
CREATE INDEX IF NOT EXISTS idx_user_thumbnails_user_id ON user_thumbnails(user_id);
CREATE INDEX IF NOT EXISTS idx_user_thumbnails_thumbnail_id ON user_thumbnails(thumbnail_id);

-- Update trigger for updated_at
DROP TRIGGER IF EXISTS update_user_thumbnails_updated_at ON user_thumbnails;
CREATE TRIGGER update_user_thumbnails_updated_at
    BEFORE UPDATE ON user_thumbnails
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

COMMIT;

