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
UPDATE account_exports
SET state = 'running', updated_at = now()
WHERE id IN (
    SELECT id FROM account_exports
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
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
