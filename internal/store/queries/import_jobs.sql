-- Asynchronous URL-import job queue (migration 0059, fix_plan P2.2). Mirrors
-- the transcode_jobs durable-queue pattern: pending → running → done | failed,
-- exponential backoff on retry, dead-letter after max attempts.

-- name: EnqueueImportJob :one
-- Single active import per video: while a pending/running row exists the insert
-- is a no-op (partial unique index) and pgx reports no row — the handler then
-- returns the in-flight job. resolver is 'direct'|'ytdlp' (concrete) or 'auto'
-- (the worker resolves it during the 'resolving' stage).
INSERT INTO import_jobs (video_id, url, resolver)
VALUES ($1, $2, $3)
ON CONFLICT (video_id) WHERE state IN ('pending', 'running') DO NOTHING
RETURNING *;

-- name: GetLatestImportJobByVideo :one
SELECT * FROM import_jobs
WHERE video_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ClaimDueImportJobs :many
-- Atomically claims due pending jobs (oldest first) by flipping them to
-- 'running', exactly like ClaimDueTranscodeJobs. resolver/stage travel with the
-- claim so the worker picks the fetch path and can report honest progress.
--
-- FOR UPDATE SKIP LOCKED is what makes this safe with more than one instance:
-- concurrent claimers take disjoint rows instead of blocking on each other and
-- then racing to re-evaluate the subquery. Without it, `UPDATE ... WHERE id IN
-- (SELECT ...)` is the classic queue anti-pattern -- the ids are chosen before
-- the lock is taken, so two claimers can select the same row.
UPDATE import_jobs
SET state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM import_jobs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, video_id, url, attempts, resolver, stage;

-- name: SetImportJobStage :exec
-- Records coarse progress for the UI while a job runs (queued → resolving →
-- downloading → processing). Never carries a secret.
UPDATE import_jobs
SET stage = $2, updated_at = now()
WHERE id = $1;

-- name: SetImportJobResolver :exec
-- Rewrites a job's resolver to the concrete choice once an 'auto' request has
-- been resolved (so the stored/echoed value is always what actually ran), and
-- moves the stage forward in the same write.
UPDATE import_jobs
SET resolver = $2, stage = $3, updated_at = now()
WHERE id = $1;

-- name: CompleteImportJob :exec
UPDATE import_jobs
SET state = 'done', error = '', stage = '', updated_at = now()
WHERE id = $1;

-- name: RescheduleImportJob :exec
-- Back to pending with backoff so a later drain retries it (stage resets to
-- queued).
UPDATE import_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, error = $3, stage = '', updated_at = now()
WHERE id = $1;

-- name: FailImportJob :exec
-- Dead-letter: no further retries (stage cleared).
UPDATE import_jobs
SET state = 'failed', attempts = attempts + 1, error = $2, stage = '', updated_at = now()
WHERE id = $1;
