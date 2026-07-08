-- 0076: Live-session start time. The public "Live now" listing (GET /api/v1/live)
-- needs a truthful "when did this live session begin" value; before this the only
-- timestamps on a stream were created_at (when the metadata row was made) and
-- updated_at (bumped by any edit or state flip) — neither is the session start.
--
-- started_at is stamped when a stream transitions offline/ended -> live (by the
-- RTMP ingest boundary, via SetLiveStreamState) and cleared when it leaves live.
-- It is NULL for a stream that has never gone live (and for streams that were live
-- before this migration, until their next go-live). Nullable by design.
ALTER TABLE live_streams
    ADD COLUMN started_at TIMESTAMPTZ;

-- The public listing is exactly: state = 'live' AND privacy = 'public', ordered by
-- started_at DESC. A partial index over just the currently-live public rows keeps
-- that rail query cheap as the table grows with historical/offline streams.
CREATE INDEX live_streams_live_public_idx
    ON live_streams (started_at DESC)
    WHERE state = 'live' AND privacy = 'public';
