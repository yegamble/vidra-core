-- Account export job queue (migration 0057, fix_plan P4). Mirrors the
-- transcode_jobs durable-queue pattern: pending → running → done | failed,
-- exponential backoff on retry, dead-letter after max attempts, plus a 7-day
-- expiry sweep for finished archives.

-- name: CreateAccountExport :one
-- Single active export per user: while a pending/running row exists the insert
-- is a no-op (partial unique index) and pgx reports no row — the service maps
-- that to ErrExportActive.
INSERT INTO account_exports (user_id)
VALUES ($1)
ON CONFLICT (user_id) WHERE state IN ('pending', 'running') DO NOTHING
RETURNING id, user_id, state, storage_key, attempts, next_attempt_at,
          last_error, expires_at, created_at, updated_at;

-- name: GetLatestAccountExportByUser :one
SELECT id, user_id, state, storage_key, attempts, next_attempt_at,
       last_error, expires_at, created_at, updated_at
FROM account_exports
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: DeleteInactiveAccountExportsByUser :many
-- Clears a user's finished/failed exports (before a re-request, and during the
-- §1 account delete alongside DeleteAccountExportsByUser). Returns the storage
-- keys so the caller can best-effort delete the archive blobs.
DELETE FROM account_exports
WHERE user_id = $1 AND state IN ('done', 'failed')
RETURNING storage_key;

-- name: DeleteAccountExportsByUser :many
DELETE FROM account_exports
WHERE user_id = $1
RETURNING storage_key;

-- name: ClaimDueAccountExports :many
-- Atomically claims due pending jobs (oldest first) by flipping them to
-- 'running', exactly like ClaimDueTranscodeJobs.
--
-- FOR UPDATE SKIP LOCKED is what makes this safe with more than one instance:
-- concurrent claimers take disjoint rows instead of blocking on each other and
-- then racing to re-evaluate the subquery. Without it, `UPDATE ... WHERE id IN
-- (SELECT ...)` is the classic queue anti-pattern -- the ids are chosen before
-- the lock is taken, so two claimers can select the same row.
UPDATE account_exports
SET next_attempt_at = now() + interval '30 minutes',
    state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM account_exports
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, user_id, attempts;

-- name: CompleteAccountExport :exec
UPDATE account_exports
SET state = 'done', storage_key = $2, expires_at = $3, updated_at = now()
WHERE id = $1;

-- name: RescheduleAccountExport :exec
-- Back to pending with backoff so a later drain retries it.
UPDATE account_exports
SET state = 'pending', attempts = attempts + 1, next_attempt_at = $2,
    last_error = $3, updated_at = now()
WHERE id = $1;

-- name: FailAccountExport :exec
-- Dead-letter: no further retries.
UPDATE account_exports
SET state = 'failed', attempts = attempts + 1, last_error = $2, updated_at = now()
WHERE id = $1;

-- name: ListExpiredAccountExports :many
-- Finished archives past their expires_at, for the sweeper.
SELECT id, storage_key
FROM account_exports
WHERE expires_at IS NOT NULL AND expires_at <= now()
ORDER BY expires_at
LIMIT $1;

-- name: DeleteAccountExport :exec
DELETE FROM account_exports WHERE id = $1;

-- name: RenewAccountExportLease :exec
-- Push a running job's lease forward. The worker calls this on a ticker while the
-- job runs, so a job that legitimately outlives one lease is not swept out from
-- under itself. Guarded on state so a completed or failed job cannot be revived.
--
-- updated_at is bumped with the lease so the operational projection can tell a
-- live job from an abandoned one — see RenewTranscodeJobLease for why that bump
-- is the whole of the `stale_running` signal.
UPDATE account_exports
SET next_attempt_at = now() + interval '30 minutes', updated_at = now()
WHERE id = $1 AND state = 'running';

-- name: SweepExpiredAccountExports :execrows
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
UPDATE account_exports
SET state = 'pending', attempts = attempts + 1, updated_at = now()
WHERE id IN (
    SELECT id FROM account_exports
    WHERE state = 'running' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT 1000
    FOR UPDATE SKIP LOCKED
);
