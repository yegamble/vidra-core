-- 0135: channel auto-sync backs off on consecutive failures.
--
-- A27 measured the gap: a sync whose source is down records state='failed' and
-- reschedules at the plain CHANNEL_SYNC_INTERVAL, so a permanently dead source
-- is re-listed at that rate forever — an unbounded, self-inflicted load on
-- somebody else's server (the same shape 0000-era peertubeimport hit with its
-- oversize images, and the same fix: stop asking so often).
--
-- failure_count is the CONSECUTIVE failure counter the worker multiplies the
-- interval by: the next attempt lands CHANNEL_SYNC_INTERVAL * 2^(n-1) after the
-- nth consecutive failure, capped at CHANNEL_SYNC_BACKOFF_MAX. It is reset to 0
-- by the first success (FinishChannelSync), so a transient outage costs a few
-- widening retries and nothing more. It lives on the row rather than in worker
-- memory so a restart mid-backoff keeps the schedule it had earned — next_run_at
-- alone would survive the restart but the DOUBLING would start over.
--
-- Additive with a default: existing rows read 0, which is exactly "no
-- consecutive failures yet", so a deploy needs no backfill.
ALTER TABLE channel_syncs
    ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0;
