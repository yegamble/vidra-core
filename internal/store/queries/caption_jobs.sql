-- Auto-caption (Whisper) job queue (migration 0062, fix_plan P13). Mirrors the
-- import_jobs durable-queue pattern: pending → running → done | failed,
-- exponential backoff on retry, dead-letter after max attempts.

-- name: EnqueueCaptionJob :one
-- Single active auto-caption per video: while a pending/running row exists the
-- insert is a no-op (partial unique index) and pgx reports no row — the handler
-- then returns 409.
INSERT INTO caption_jobs (video_id, language)
VALUES ($1, $2)
ON CONFLICT (video_id) WHERE state IN ('pending', 'running') DO NOTHING
RETURNING *;

-- name: GetLatestCaptionJobByVideo :one
SELECT * FROM caption_jobs
WHERE video_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ClaimDueCaptionJobs :many
-- Atomically claims due pending jobs (oldest first) by flipping them to
-- 'running', exactly like ClaimDueImportJobs.
UPDATE caption_jobs
SET state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM caption_jobs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
)
RETURNING id, video_id, language, attempts;

-- name: CompleteCaptionJob :exec
UPDATE caption_jobs
SET state = 'done', error = '', updated_at = now()
WHERE id = $1;

-- name: RescheduleCaptionJob :exec
-- Back to pending with backoff so a later drain retries it.
UPDATE caption_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, error = $3, updated_at = now()
WHERE id = $1;

-- name: FailCaptionJob :exec
-- Dead-letter: no further retries.
UPDATE caption_jobs
SET state = 'failed', attempts = attempts + 1, error = $2, updated_at = now()
WHERE id = $1;
