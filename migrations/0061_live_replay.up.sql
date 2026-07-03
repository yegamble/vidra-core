-- 0061: Live replay-to-VOD. A channel owner may opt a live stream into recording
-- its sessions and republishing them as a normal on-demand video (P12 replay
-- conversion). When `replay_enabled` is true, the RTMP media server records the
-- session and, on ingest-stop, the api best-effort creates a draft video on the
-- stream's channel and runs the recording through the normal upload pipeline
-- (scan/probe/thumbnail/transcode). Default off — recording is opt-in.
ALTER TABLE live_streams
    ADD COLUMN replay_enabled BOOLEAN NOT NULL DEFAULT false;
