-- Per-video playback aggregates (migration 0174). No raw samples are stored:
-- the beacon handler folds each sample into these daily counters.

-- name: UpsertVideoPlaybackSample :exec
-- Folds ONE beacon into today's aggregates in a single statement, so a partial
-- failure cannot leave plays/watch seconds disagreeing with the retention and
-- device counters. Dimension rows are written only when watch_seconds > 0.
WITH d AS (
    INSERT INTO video_playback_days (video_id, day, plays, watch_seconds)
    VALUES (sqlc.arg('video_id')::uuid, (now() AT TIME ZONE 'utc')::date, sqlc.arg('plays')::bigint, sqlc.arg('watch_seconds')::bigint)
    ON CONFLICT (video_id, day) DO UPDATE
    SET plays = video_playback_days.plays + EXCLUDED.plays,
        watch_seconds = video_playback_days.watch_seconds + EXCLUDED.watch_seconds
)
INSERT INTO video_playback_dims (video_id, day, kind, key, value)
SELECT sqlc.arg('video_id')::uuid, (now() AT TIME ZONE 'utc')::date, t.kind, t.key, sqlc.arg('watch_seconds')::bigint
FROM (VALUES
    ('retention', sqlc.arg('retention_key')::text),
    ('device', sqlc.arg('device')::text),
    ('os', sqlc.arg('os')::text)
) AS t(kind, key)
WHERE sqlc.arg('watch_seconds')::bigint > 0
ON CONFLICT (video_id, day, kind, key) DO UPDATE
SET value = video_playback_dims.value + EXCLUDED.value;

-- name: ListVideoPlaybackDays :many
SELECT day, plays, watch_seconds
FROM video_playback_days
WHERE video_id = $1 AND day >= sqlc.arg('since')::date
ORDER BY day;

-- name: SumVideoPlaybackDims :many
SELECT kind, key, SUM(value)::bigint AS value
FROM video_playback_dims
WHERE video_id = $1 AND day >= sqlc.arg('since')::date
GROUP BY kind, key
ORDER BY kind, key;

-- name: PruneVideoPlaybackDays :execrows
DELETE FROM video_playback_days WHERE day < sqlc.arg('before')::date;

-- name: PruneVideoPlaybackDims :execrows
DELETE FROM video_playback_dims WHERE day < sqlc.arg('before')::date;
