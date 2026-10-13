-- Per-video playback aggregates (migration 0174). No raw samples are stored:
-- the beacon handler folds each sample into these daily counters.

-- name: UpsertVideoPlaybackDay :exec
INSERT INTO video_playback_days (video_id, day, plays, watch_seconds)
VALUES (sqlc.arg('video_id'), (now() AT TIME ZONE 'utc')::date, sqlc.arg('plays'), sqlc.arg('watch_seconds'))
ON CONFLICT (video_id, day) DO UPDATE
SET plays = video_playback_days.plays + EXCLUDED.plays,
    watch_seconds = video_playback_days.watch_seconds + EXCLUDED.watch_seconds;

-- name: UpsertVideoPlaybackDim :exec
INSERT INTO video_playback_dims (video_id, day, kind, key, value)
VALUES (sqlc.arg('video_id'), (now() AT TIME ZONE 'utc')::date, sqlc.arg('kind'), sqlc.arg('key'), sqlc.arg('value'))
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
