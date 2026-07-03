DROP TABLE remote_video_blocks;

DROP INDEX IF EXISTS reports_reporter_remote_video_idx;
DELETE FROM reports WHERE target_type = 'remote_video';
ALTER TABLE reports DROP COLUMN remote_video_id;

ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('video', 'comment', 'account'));
