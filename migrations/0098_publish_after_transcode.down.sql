DROP INDEX videos_transcoding_hold_idx;
-- 'transcoding' rows must leave the state set before the narrower CHECK returns.
-- Publishing them is the safe direction (the original is playable), mirroring the
-- feature's terminal-failure/timeout release.
UPDATE videos SET state = 'published' WHERE state = 'transcoding';
ALTER TABLE videos DROP CONSTRAINT videos_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN ('draft', 'processing', 'scheduled', 'quarantined', 'published', 'failed'));
ALTER TABLE videos DROP COLUMN publish_after_transcode;
