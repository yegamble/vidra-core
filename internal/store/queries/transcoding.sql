-- HLS transcoding pipeline (migration 0039): durable transcode job queue plus
-- streaming-playlist/rendition bookkeeping. Mirrors federation_deliveries.

-- name: EnqueueTranscodeJob :exec
-- Idempotent per live job: at most one pending/running job per video (partial
-- unique index), so a re-upload while one is queued does not double the work.
INSERT INTO transcode_jobs (video_id, source_key)
VALUES ($1, $2)
ON CONFLICT (video_id) WHERE state IN ('pending', 'running') DO NOTHING;

-- name: ClaimDueTranscodeJobs :many
-- Atomically claims due pending jobs (oldest first) by flipping them to
-- 'running'. A single in-process worker drains sequentially; the claim still
-- guards against double-processing across restarts within one batch.
UPDATE transcode_jobs
SET state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM transcode_jobs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
)
RETURNING id, video_id, source_key, attempts;

-- name: CompleteTranscodeJob :exec
UPDATE transcode_jobs
SET state = 'done', updated_at = now()
WHERE id = $1;

-- name: RescheduleTranscodeJob :exec
-- Back to pending with backoff so a later drain retries it.
UPDATE transcode_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE id = $1;

-- name: FailTranscodeJob :exec
-- Dead-letter: no further retries.
UPDATE transcode_jobs
SET state = 'failed', attempts = attempts + 1, last_error = $2, updated_at = now()
WHERE id = $1;

-- name: UpsertStreamingPlaylist :one
INSERT INTO streaming_playlists (video_id, master_key, state)
VALUES ($1, $2, $3)
ON CONFLICT (video_id) DO UPDATE
SET master_key = EXCLUDED.master_key, state = EXCLUDED.state, updated_at = now()
RETURNING *;

-- name: GetStreamingPlaylist :one
SELECT * FROM streaming_playlists WHERE video_id = $1;

-- name: CreateVideoRendition :one
INSERT INTO video_renditions (video_id, height, width, key_prefix)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteVideoRenditions :exec
DELETE FROM video_renditions WHERE video_id = $1;

-- name: ListVideoRenditions :many
SELECT * FROM video_renditions WHERE video_id = $1 ORDER BY height DESC;
