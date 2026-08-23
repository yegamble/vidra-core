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
--
-- FOR UPDATE SKIP LOCKED is what makes this safe with more than one instance:
-- concurrent claimers take disjoint rows instead of blocking on each other and
-- then racing to re-evaluate the subquery. Without it, `UPDATE ... WHERE id IN
-- (SELECT ...)` is the classic queue anti-pattern -- the ids are chosen before
-- the lock is taken, so two claimers can select the same row.
UPDATE caption_jobs
SET next_attempt_at = now() + interval '30 minutes',
    state = 'running', stage = 'preparing', progress_percent = 5, updated_at = now()
WHERE id IN (
    SELECT id FROM caption_jobs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, video_id, language, attempts;

-- name: SetCaptionJobProgress :exec
UPDATE caption_jobs
SET stage = $2, progress_percent = $3, updated_at = now()
WHERE id = $1;

-- name: CompleteCaptionJob :exec
UPDATE caption_jobs
SET state = 'done', error = '', stage = 'complete', progress_percent = 100, updated_at = now()
WHERE id = $1;

-- name: RescheduleCaptionJob :exec
-- Back to pending with backoff so a later drain retries it.
UPDATE caption_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, error = $3,
    stage = '', progress_percent = 0, updated_at = now()
WHERE id = $1;

-- name: FailCaptionJob :exec
-- Dead-letter: no further retries.
UPDATE caption_jobs
SET state = 'failed', attempts = attempts + 1, error = $2, stage = 'failed', updated_at = now()
WHERE id = $1;

-- name: RenewCaptionJobLease :exec
-- Push a running job's lease forward. The worker calls this on a ticker while the
-- job runs, so a job that legitimately outlives one lease is not swept out from
-- under itself. Guarded on state so a completed or failed job cannot be revived.
--
-- updated_at is bumped with the lease so the operational projection can tell a
-- live job from an abandoned one — see RenewTranscodeJobLease for why that bump
-- is the whole of the `stale_running` signal.
UPDATE caption_jobs
SET next_attempt_at = now() + interval '30 minutes', updated_at = now()
WHERE id = $1 AND state = 'running';

-- name: SweepExpiredCaptionJobs :execrows
-- Return jobs whose lease elapsed while they were 'running' to the queue.
--
-- This REPLACES the boot-time blanket requeue of every running row, which was
-- safe only because the process doing it was the deployment's only worker. A
-- second instance booting would have requeued jobs the first was actively
-- running. A lease sweep needs no such assumption: it only touches rows whose
-- owner has demonstrably stopped renewing, so it is correct with any number of
-- instances and can run periodically rather than only at start-up.
--
-- attempts is incremented so a job that crashes its worker every time walks its
-- counter up and dead-letters through the normal path instead of looping forever.
--
-- Bounded FOR UPDATE SKIP LOCKED for the same reason the claim uses it: without
-- it, concurrent sweepers take their row locks in table order and serialise on
-- each other; with it they take disjoint rows. LIMIT bounds one tick's work
-- (see SweepExpiredTranscodeJobs for the full rationale).
UPDATE caption_jobs
SET state = 'pending', attempts = attempts + 1, stage = '', progress_percent = 0, updated_at = now()
WHERE id IN (
    SELECT id FROM caption_jobs
    WHERE state = 'running' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT 1000
    FOR UPDATE SKIP LOCKED
);
