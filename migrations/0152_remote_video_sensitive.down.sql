-- 0152 down: drop the remote-video sensitive flag.
ALTER TABLE remote_videos DROP COLUMN IF EXISTS is_sensitive;
