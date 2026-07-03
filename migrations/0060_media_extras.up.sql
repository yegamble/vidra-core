-- 0060: media extras for the storyboard/VP9 + playlist-thumbnail slice
-- (core-media-2).
--
-- 1. Widen the video_files.kind CHECK so the storyboard sprite sheet
--    ('storyboard'), its WebVTT sprite map ('storyboard_vtt'), and an optional
--    progressive VP9/WebM alternate ('webm') can be stored alongside the
--    original/thumbnail/rendition files. Postgres named the inline CHECK from
--    0008 "video_files_kind_check" (widened once already in 0010).
-- 2. Add playlists.thumbnail_ext: the file extension (jpg/png/webp) of an
--    owner-uploaded playlist cover, NULL when none. The blob lives at
--    playlist-thumbnails/<id>.<ext>; has_thumbnail = thumbnail_ext IS NOT NULL.

ALTER TABLE video_files DROP CONSTRAINT IF EXISTS video_files_kind_check;
ALTER TABLE video_files
    ADD CONSTRAINT video_files_kind_check
    CHECK (kind IN ('original', 'rendition', 'thumbnail', 'storyboard', 'storyboard_vtt', 'webm'));

ALTER TABLE playlists ADD COLUMN thumbnail_ext TEXT;
