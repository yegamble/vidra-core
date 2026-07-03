-- Down 0060: drop the playlist cover column and narrow the video_files kind
-- CHECK back to its 0010 shape. Any storyboard/webm rows must be removed first
-- so the narrowed CHECK can be re-added.

ALTER TABLE playlists DROP COLUMN IF EXISTS thumbnail_ext;

DELETE FROM video_files WHERE kind IN ('storyboard', 'storyboard_vtt', 'webm');

ALTER TABLE video_files DROP CONSTRAINT IF EXISTS video_files_kind_check;
ALTER TABLE video_files
    ADD CONSTRAINT video_files_kind_check
    CHECK (kind IN ('original', 'rendition', 'thumbnail'));
