DROP INDEX IF EXISTS video_files_unhashed_idx;
ALTER TABLE video_files DROP COLUMN IF EXISTS sha256;
