-- name: UpsertVideoMetadata :one
INSERT INTO video_metadata (video_id, duration_seconds, width, height, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (video_id) DO UPDATE
SET duration_seconds = EXCLUDED.duration_seconds,
    width            = EXCLUDED.width,
    height           = EXCLUDED.height,
    updated_at       = now()
RETURNING video_id, duration_seconds, width, height, updated_at;

-- name: GetVideoMetadata :one
SELECT video_id, duration_seconds, width, height, updated_at
FROM video_metadata
WHERE video_id = $1;

-- name: FillVideoDurationIfUnknown :execrows
-- The transcode's measurement of a duration the probe could not read (a WebM
-- written to a pipe states none). FILL-ONLY: never overwrites a probed or
-- imported value, so a rerun is a no-op. Width/height are left alone.
INSERT INTO video_metadata (video_id, duration_seconds, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (video_id) DO UPDATE
SET duration_seconds = EXCLUDED.duration_seconds,
    updated_at       = now()
WHERE video_metadata.duration_seconds IS NULL;
