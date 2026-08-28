-- Asynchronous upload-session completion queue (migration 0120). Mirrors the
-- import_jobs / transcode_jobs durable-queue pattern: pending → running → done |
-- failed, lease-on-claim with FOR UPDATE SKIP LOCKED, exponential backoff on
-- retry, dead-letter after max attempts.
--
-- It exists because completion is EXPENSIVE — assembling every chunk back out of
-- object storage, re-uploading the assembled file while hashing it, then probing
-- and decoding it for the thumbnail/storyboard — and completion used to run all
-- of that inside a request carrying the general 30s deadline, behind a CDN that
-- caps origin response time. See 0120 for the whole story.

-- name: EnqueueUploadFinalizeJob :one
-- Single active finalize per session: while a pending/running row exists the
-- insert is a no-op (partial unique index) and pgx reports no row — the handler
-- then answers 202 with the session's current state rather than queueing the
-- pipeline twice. That is the idempotency guarantee for a client that retries a
-- completion whose response it never saw.
INSERT INTO upload_finalize_jobs (upload_id, video_id, purpose, can_manage)
VALUES ($1, $2, $3, $4)
ON CONFLICT (upload_id) WHERE state IN ('pending', 'running') DO NOTHING
RETURNING *;

-- name: GetLatestUploadFinalizeJob :one
SELECT * FROM upload_finalize_jobs
WHERE upload_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: HasLiveUploadFinalizeJob :one
-- Whether a pending/running finalize job exists for the session (served by the
-- partial unique index that makes the enqueue idempotent).
--
-- It exists for the REPORTED state, not for any control decision. Accepting a
-- completion is two statements — insert the job, then flip the session to
-- 'queued' — and a concurrent second POST is released by the index the moment
-- the insert commits, which is before that flip lands. Reporting the raw row
-- then says 'active' for an upload that is queued and about to publish, and a
-- client reads 'active' after a completion as "this upload is gone".
SELECT EXISTS (
    SELECT 1 FROM upload_finalize_jobs
    WHERE upload_id = $1 AND state IN ('pending', 'running')
);

-- name: DeleteUploadFinalizeJob :exec
-- Drop a job that was enqueued but never admitted: the session stopped being
-- active between the completion's validation and its state transition (a DELETE
-- raced it), so the pipeline must not run.
--
-- Deliberately a DELETE rather than FailUploadFinalizeJob. Dead-lettering would
-- publish a 'failed' upload_finalize run into the operational projection for work
-- that never started and never could have, putting a phantom failure in front of
-- the operator; the row is also what the session sweeper's NOT EXISTS looks at,
-- so removing it lets the cancelled session be collected normally.
DELETE FROM upload_finalize_jobs WHERE id = $1;

-- name: ClaimDueUploadFinalizeJobs :many
-- Atomically claims due pending jobs (oldest first) by flipping them to
-- 'running', exactly like ClaimDueImportJobs.
--
-- FOR UPDATE SKIP LOCKED is what makes this safe with more than one instance:
-- concurrent claimers take disjoint rows instead of blocking on each other and
-- then racing to re-evaluate the subquery. Without it, `UPDATE ... WHERE id IN
-- (SELECT ...)` is the classic queue anti-pattern -- the ids are chosen before
-- the lock is taken, so two claimers can select the same row.
UPDATE upload_finalize_jobs
SET next_attempt_at = now() + interval '30 minutes',
    state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM upload_finalize_jobs
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, upload_id, video_id, purpose, can_manage, attempts;

-- name: CompleteUploadFinalizeJob :exec
UPDATE upload_finalize_jobs
SET state = 'done', error = '', updated_at = now()
WHERE id = $1;

-- name: RescheduleUploadFinalizeJob :exec
-- Back to pending with backoff so a later drain retries it. The assembled bytes
-- are re-streamed from the chunk blobs, which are still in place: nothing is
-- deleted until the session reaches a terminal state.
UPDATE upload_finalize_jobs
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2, error = $3, updated_at = now()
WHERE id = $1;

-- name: FailUploadFinalizeJob :exec
-- Dead-letter: no further retries. The caller also marks the session 'failed'
-- with the same safe reason, which is what the client's poll surfaces.
UPDATE upload_finalize_jobs
SET state = 'failed', attempts = attempts + 1, error = $2, updated_at = now()
WHERE id = $1;

-- name: RenewUploadFinalizeJobLease :exec
-- Push a running job's lease forward. The worker calls this on a ticker while the
-- job runs, so a job that legitimately outlives one lease is not swept out from
-- under itself -- and a large upload's assembly + probe over a remote object
-- store is exactly the work that does. Guarded on state so a completed or failed
-- job cannot be revived.
--
-- updated_at is bumped with the lease so the operational projection can tell a
-- live job from an abandoned one -- see RenewTranscodeJobLease for why that bump
-- is the whole of the `stale_running` signal.
UPDATE upload_finalize_jobs
SET next_attempt_at = now() + interval '30 minutes', updated_at = now()
WHERE id = $1 AND state = 'running';

-- name: SweepExpiredUploadFinalizeJobs :execrows
-- Return jobs whose lease elapsed while they were 'running' to the queue.
--
-- Without it a finalize stranded by a dead worker is permanent AND invisible:
-- the partial unique index counts the 'running' row as live, so a client
-- re-POSTing complete gets 202 forever against a job nobody will ever run, and
-- the session sits in 'processing' with the video stuck in 'draft'.
--
-- attempts is incremented so a job that crashes its worker every time walks its
-- counter up and dead-letters through the normal path instead of looping forever.
--
-- Bounded FOR UPDATE SKIP LOCKED for the same reason the claim uses it: without
-- it, concurrent sweepers take their row locks in table order and serialise on
-- each other; with it they take disjoint rows (see SweepExpiredTranscodeJobs).
UPDATE upload_finalize_jobs
SET state = 'pending', attempts = attempts + 1, updated_at = now()
WHERE id IN (
    SELECT id FROM upload_finalize_jobs
    WHERE state = 'running' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT 1000
    FOR UPDATE SKIP LOCKED
);
