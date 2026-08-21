-- HLS transcoding pipeline (migration 0039): durable transcode job queue plus
-- streaming-playlist/rendition bookkeeping. Mirrors federation_deliveries.

-- name: EnqueueTranscodeJob :exec
-- Idempotent per live job: at most one pending/running job per video (partial
-- unique index), so a re-upload while one is queued does not double the work.
INSERT INTO transcode_jobs (video_id, source_key, transcode_type)
VALUES ($1, $2, $3)
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
RETURNING id, video_id, source_key, transcode_type, attempts;

-- name: HasLiveTranscodeJob :one
-- Whether a pending/running transcode job exists for the video. The video-file
-- replacement flow (config-parity W14) answers 409 replace_conflict while one
-- does: enqueueing is idempotent per live job, so a replacement accepted now
-- would silently never be transcoded.
SELECT EXISTS (
    SELECT 1 FROM transcode_jobs
    WHERE video_id = $1 AND state IN ('pending', 'running')
);

-- name: DeleteStreamingPlaylist :exec
-- Drops a video's playlist row (with DeleteVideoRenditions) when a replacement
-- lands while transcoding is unavailable (config-parity W14): the old HLS tree
-- must stop serving superseded content; playback falls back to the progressive
-- original until a future transcode re-promotes a ladder.
DELETE FROM streaming_playlists WHERE video_id = $1;

-- name: CompleteTranscodeJob :exec
UPDATE transcode_jobs
SET state = 'done', updated_at = now()
WHERE id = $1;

-- name: RescheduleTranscodeJob :exec
-- Back to pending with backoff so a later drain retries it.
UPDATE transcode_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE id = $1;

-- name: DeferTranscodeJob :exec
-- Return a claimed job to the queue WITHOUT consuming an attempt. Used when the
-- job cannot run for a reason that has nothing to do with the job itself -- today
-- only "the scratch filesystem does not have room for this source".
--
-- Deliberately NOT RescheduleTranscodeJob: that increments attempts, so a video
-- that happened to arrive during five full-disk ticks would dead-letter and stay
-- untranscoded forever. A transient host condition must not consume a video's
-- retry budget.
UPDATE transcode_jobs
SET state = 'pending', next_attempt_at = $2, last_error = $3, updated_at = now()
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
INSERT INTO video_renditions (video_id, height, width, key_prefix, size_bytes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteVideoRenditions :exec
DELETE FROM video_renditions WHERE video_id = $1;

-- name: ListVideoRenditions :many
SELECT * FROM video_renditions WHERE video_id = $1 ORDER BY height DESC;

-- name: UpsertTranscodeStep :exec
INSERT INTO transcode_steps (
    transcode_job_id, video_id, format, height, width, state, stage,
    progress_percent, started_at, finished_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    CASE WHEN $6 = 'running' THEN now() ELSE NULL END,
    CASE WHEN $6 IN ('succeeded', 'failed') THEN now() ELSE NULL END
)
ON CONFLICT (transcode_job_id, format, height) DO UPDATE SET
    width = EXCLUDED.width,
    state = EXCLUDED.state,
    stage = EXCLUDED.stage,
    progress_percent = EXCLUDED.progress_percent,
    started_at = CASE
        WHEN transcode_steps.started_at IS NULL AND EXCLUDED.state = 'running' THEN now()
        ELSE transcode_steps.started_at
    END,
    finished_at = CASE
        WHEN EXCLUDED.state IN ('succeeded', 'failed') THEN now()
        ELSE NULL
    END,
    updated_at = now();

-- name: GetVideoFileSizeByStorageKey :one
-- The stored byte size of the file at a storage key. The transcode worker uses it
-- for scratch-space admission control: a job's source_key IS the original's
-- storage_key, so this answers "how big is the thing I am about to transcode"
-- without a round trip to the object store. Rows are unique enough in practice
-- (one file per stored key) but LIMIT 1 keeps the query total.
SELECT size_bytes FROM video_files WHERE storage_key = $1 LIMIT 1;
