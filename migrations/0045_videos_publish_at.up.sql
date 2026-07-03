-- 0045: scheduled publish (product-decisions.md §17). publish_at is settable
-- while a video is not yet published; a processed video whose publish_at lies
-- in the future parks in the new 'scheduled' state instead of 'published'.
-- Public surfaces already filter on state = 'published', so scheduled videos
-- stay off feeds/search until the in-process sweeper transitions them through
-- the same publish path Process uses (federation announce + transcode enqueue
-- hooks fire at that moment).
ALTER TABLE videos ADD COLUMN publish_at TIMESTAMPTZ;

ALTER TABLE videos DROP CONSTRAINT videos_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN ('draft', 'processing', 'scheduled', 'published', 'failed'));

-- The sweeper's "due" scan: only scheduled rows, ordered by when they are due.
CREATE INDEX videos_due_publish_idx ON videos (publish_at) WHERE state = 'scheduled';
