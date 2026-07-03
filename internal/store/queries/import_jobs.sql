-- Asynchronous URL-import job queue (migration 0059, fix_plan P2.2). Mirrors
-- the transcode_jobs durable-queue pattern: pending → running → done | failed,
-- exponential backoff on retry, dead-letter after max attempts.

-- name: EnqueueImportJob :one
-- Single active import per video: while a pending/running row exists the insert
-- is a no-op (partial unique index) and pgx reports no row — the handler then
-- returns the in-flight job.
INSERT INTO import_jobs (video_id, url)
VALUES ($1, $2)
ON CONFLICT (video_id) WHERE state IN ('pending', 'running') DO NOTHING
RETURNING *;

-- name: GetLatestImportJobByVideo :one
SELECT * FROM import_jobs
WHERE video_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ClaimDueImportJobs :many
-- Atomically claims due pending jobs (oldest first) by flipping them to
-- 'running', exactly like ClaimDueTranscodeJobs.
UPDATE import_jobs
SET state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM import_jobs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
)
RETURNING id, video_id, url, attempts;

-- name: CompleteImportJob :exec
UPDATE import_jobs
SET state = 'done', error = '', updated_at = now()
WHERE id = $1;

-- name: RescheduleImportJob :exec
-- Back to pending with backoff so a later drain retries it.
UPDATE import_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, error = $3, updated_at = now()
WHERE id = $1;

-- name: FailImportJob :exec
-- Dead-letter: no further retries.
UPDATE import_jobs
SET state = 'failed', attempts = attempts + 1, error = $2, updated_at = now()
WHERE id = $1;
