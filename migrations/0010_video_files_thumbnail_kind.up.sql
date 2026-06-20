-- 0010: allow 'thumbnail' as a video_files kind, so a generated poster image can
-- be stored alongside the original and (later) renditions. Postgres names the
-- inline column CHECK from 0008 "video_files_kind_check".

ALTER TABLE video_files DROP CONSTRAINT IF EXISTS video_files_kind_check;
ALTER TABLE video_files
    ADD CONSTRAINT video_files_kind_check
    CHECK (kind IN ('original', 'rendition', 'thumbnail'));
