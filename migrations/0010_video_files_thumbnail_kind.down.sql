-- Revert to the original/rendition-only kinds. Any stored thumbnails must go
-- first so the narrower constraint can be re-applied.
ALTER TABLE video_files DROP CONSTRAINT IF EXISTS video_files_kind_check;
DELETE FROM video_files WHERE kind = 'thumbnail';
ALTER TABLE video_files
    ADD CONSTRAINT video_files_kind_check
    CHECK (kind IN ('original', 'rendition'));
