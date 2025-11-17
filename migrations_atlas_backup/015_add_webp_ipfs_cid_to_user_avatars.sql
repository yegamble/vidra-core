-- Add webp_ipfs_cid column to user_avatars table for WebP variant storage
ALTER TABLE user_avatars
    ADD COLUMN webp_ipfs_cid TEXT;