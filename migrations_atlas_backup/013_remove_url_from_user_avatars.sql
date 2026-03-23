-- Remove url column from user_avatars now that avatar is tracked by file_id and ipfs_cid
ALTER TABLE user_avatars
    DROP COLUMN IF EXISTS url;
