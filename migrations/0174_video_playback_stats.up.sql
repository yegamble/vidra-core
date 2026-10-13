-- Per-video playback analytics (watch time, audience retention, device split).
--
-- PRIVACY BY CONSTRUCTION: there is no raw-sample table and no viewer, session,
-- IP or User-Agent column anywhere. The beacon handler folds each sample
-- straight into these two daily aggregates and forgets it, so there is nothing
-- per-viewer to leak, subpoena or prune. Storage is bounded by
-- (videos x days x ~30 dimension keys) and old days are pruned by
-- playbackstats.Service.Prune.

CREATE TABLE video_playback_days (
    video_id      UUID   NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    day           DATE   NOT NULL,
    plays         BIGINT NOT NULL DEFAULT 0,
    watch_seconds BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (video_id, day)
);

-- One generic counter table for the three small closed dimensions, so a new
-- dimension is a vocabulary change rather than a migration:
--   retention: key '00'..'19', value = seconds watched while the playhead was in
--              that twentieth of the video
--   device:    key mobile|desktop|tv|other, value = seconds watched
--   os:        key ios|android|windows|macos|linux|chromeos|other, value = seconds
CREATE TABLE video_playback_dims (
    video_id UUID   NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    day      DATE   NOT NULL,
    kind     TEXT   NOT NULL CHECK (kind IN ('retention', 'device', 'os')),
    key      TEXT   NOT NULL CHECK (char_length(key) <= 16),
    value    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (video_id, day, kind, key)
);

-- Retention pruning scans by day alone.
CREATE INDEX video_playback_days_day_idx ON video_playback_days (day);
CREATE INDEX video_playback_dims_day_idx ON video_playback_dims (day);
