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
