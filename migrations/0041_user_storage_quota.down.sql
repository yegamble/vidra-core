-- 0041 down: drop the per-user storage quota override.
ALTER TABLE users DROP COLUMN IF EXISTS storage_quota_bytes;
