-- 0168: Planned start time for a live stream (PeerTube 7.3 live schedules).
--
-- scheduled_start_at is ANNOUNCEMENT metadata only: "this stream plans to go
-- live at T". Nothing reads it to start, stop or flip a stream — going live is
-- still, and only, the RTMP publish callback (SetLiveStreamState). It is the
-- live counterpart of videos.publish_at (0045) but deliberately has no sweeper:
-- a VOD can be published by a clock because the bytes already exist; a live
-- cannot, because the broadcaster has to actually connect.
--
-- Nullable: NULL = unscheduled (every existing row). The go-live transition
-- clears it (SetLiveStreamState), so a permanent stream that is reused does not
-- keep announcing a start time that has already happened.
ALTER TABLE live_streams
    ADD COLUMN scheduled_start_at TIMESTAMPTZ;

-- The public "Upcoming" listing is exactly: offline + public + scheduled,
-- soonest first. A partial index over just those rows keeps the rail cheap as
-- the table accumulates historical streams.
CREATE INDEX live_streams_upcoming_idx
    ON live_streams (scheduled_start_at)
    WHERE scheduled_start_at IS NOT NULL AND state = 'offline' AND privacy = 'public';
