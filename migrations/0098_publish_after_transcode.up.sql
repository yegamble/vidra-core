-- 0098: publish-after-transcode. A per-video opt-in: when set, a processed video
-- parks in the new 'transcoding' state instead of publishing, staying hidden from
-- EVERY public surface (feeds/search/channel timeline/federation/ATProto all
-- filter on state = 'published') until its HLS transcode finishes. The transcode
-- completion hook (last job done) then releases it through the same publish
-- transition Process uses, so federation announce + ATProto cross-post + search
-- index all fire at real publish time. The owner and moderators still see the
-- held video (badged by its state). When transcoding is disabled/unavailable the
-- flag is ignored and the video publishes immediately — a video is never parked
-- behind a gate that cannot open. A terminal transcode failure and a stuck-hold
-- timeout sweeper are the two release safety nets (publishing anyway: the
-- retained original is playable).
ALTER TABLE videos ADD COLUMN publish_after_transcode BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE videos DROP CONSTRAINT videos_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN ('draft', 'processing', 'scheduled', 'quarantined', 'transcoding', 'published', 'failed'));

-- The hold sweeper's stuck scan: only held rows, oldest hold first. updated_at is
-- the moment the video entered the hold (SetVideoState bumps it) and a held video
-- is not otherwise mutated, so it is the effective hold-start timestamp.
CREATE INDEX videos_transcoding_hold_idx ON videos (updated_at) WHERE state = 'transcoding';
