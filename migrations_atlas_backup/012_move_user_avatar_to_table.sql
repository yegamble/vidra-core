-- Move user avatar from users table to dedicated user_avatars table
-- Provides storage for avatar file UUID and IPFS CID

-- Create user_avatars table
CREATE TABLE user_avatars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    file_id UUID,
    ipfs_cid TEXT,
    url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Keep updated_at current
CREATE TRIGGER update_user_avatars_updated_at
    BEFORE UPDATE ON user_avatars
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Backfill existing data from users.avatar into user_avatars.url
INSERT INTO user_avatars (user_id, url)
SELECT id AS user_id, COALESCE(avatar, '') AS url
FROM users
WHERE avatar IS NOT NULL AND avatar <> ''
ON CONFLICT (user_id) DO UPDATE SET url = EXCLUDED.url, updated_at = NOW();

-- Drop avatar column from users
ALTER TABLE users DROP COLUMN IF EXISTS avatar;

