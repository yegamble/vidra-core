-- 0170: PeerTube-style predefined abuse reasons and an optional video
-- timestamp range on reports. Both are optional structure on top of the free
-- text `reason`, which stays required. The reason vocabulary is validated in
-- the application (a new PeerTube reason must not need a migration); the
-- range invariants are cheap enough to hold in the schema as well, so a bug in
-- one writer cannot store start > end or a range on a non-video report.
ALTER TABLE reports
    ADD COLUMN predefined_reasons TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN video_start_seconds INT,
    ADD COLUMN video_end_seconds INT,
    ADD CONSTRAINT reports_video_range_chk CHECK (
        (video_start_seconds IS NULL AND video_end_seconds IS NULL)
        OR (target_type = 'video' AND video_start_seconds IS NOT NULL AND video_start_seconds >= 0
            AND (video_end_seconds IS NULL OR video_end_seconds > video_start_seconds))
    );

-- The moderation queue's reason filter (`= ANY(predefined_reasons)`).
CREATE INDEX reports_predefined_reasons_idx ON reports USING GIN (predefined_reasons);
