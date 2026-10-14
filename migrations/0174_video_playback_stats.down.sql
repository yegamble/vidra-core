-- Revert 0174. Playback aggregates are derived telemetry; dropping them loses
-- only creator analytics history, never viewer or video data.
DROP TABLE IF EXISTS video_playback_dims;
DROP TABLE IF EXISTS video_playback_days;
