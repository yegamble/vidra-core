-- Channel auto-sync (migration 0081, backport W2.C4, UPLOAD-13). A channel_sync
-- mirrors an external platform channel's uploads into a local channel; a periodic
-- worker lists the remote channel and enqueues `ytdlp` import jobs for unseen
-- entries. last_error is always a SAFE, client-visible reason.

-- name: CreateChannelSync :one
-- A duplicate (channel_id, external_channel_url) violates the UNIQUE constraint;
-- the service maps 23505 to a conflict rather than silently returning the old row.
INSERT INTO channel_syncs (channel_id, user_id, external_channel_url)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListChannelSyncsByUser :many
SELECT * FROM channel_syncs
WHERE user_id = $1
ORDER BY created_at DESC, id DESC;

-- name: GetChannelSyncByID :one
SELECT * FROM channel_syncs
WHERE id = $1;

-- name: CountChannelSyncsByUser :one
SELECT count(*) FROM channel_syncs
WHERE user_id = $1;

-- name: DeleteChannelSync :exec
DELETE FROM channel_syncs
WHERE id = $1;

-- name: TriggerChannelSyncNow :exec
-- Manual trigger: schedule the sync to run on the next tick. A no-op while it is
-- already 'syncing' (the claim below excludes that state, and the running pass
-- reschedules on completion).
UPDATE channel_syncs
SET next_run_at = now(), updated_at = now()
WHERE id = $1;

-- name: ClaimDueChannelSyncs :many
-- Atomically claims due syncs (oldest first) by flipping them to 'syncing', so an
-- overlapping tick never double-processes one. A failed sync is retryable.
--
-- FOR UPDATE SKIP LOCKED is what makes this safe with more than one instance:
-- concurrent claimers take disjoint rows instead of blocking on each other and
-- then racing to re-evaluate the subquery. Without it, `UPDATE ... WHERE id IN
-- (SELECT ...)` is the classic queue anti-pattern -- the ids are chosen before
-- the lock is taken, so two claimers can select the same row.
UPDATE channel_syncs
SET next_run_at = now() + interval '30 minutes',
    state = 'syncing', updated_at = now()
WHERE id IN (
    SELECT id FROM channel_syncs
    WHERE next_run_at <= now() AND state IN ('waiting_first_run', 'idle', 'failed')
    ORDER BY next_run_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, channel_id, user_id, external_channel_url;

-- name: FinishChannelSync :exec
-- Success: back to idle, stamp last_sync_at, clear last_error, and reschedule for
-- the next cadence (next_run_at computed by the worker).
UPDATE channel_syncs
SET state = 'idle', last_sync_at = now(), last_error = '', next_run_at = $2, updated_at = now()
WHERE id = $1;

-- name: FailChannelSync :exec
-- Failure: mark failed with a SAFE reason and reschedule for a retry on the next
-- cadence (next_run_at computed by the worker).
UPDATE channel_syncs
SET state = 'failed', last_error = $2, next_run_at = $3, updated_at = now()
WHERE id = $1;

-- name: InsertChannelSyncSeen :execrows
-- Records that an external video id has been handled by a sync. ON CONFLICT DO
-- NOTHING makes it the atomic dedupe claim: a returned affected-row count of 1
-- means this is the FIRST time we have seen the id (import it); 0 means it was
-- already handled (skip).
INSERT INTO channel_sync_seen (sync_id, external_id)
VALUES ($1, $2)
ON CONFLICT (sync_id, external_id) DO NOTHING;

-- name: RenewChannelSyncLease :exec
-- Push a syncing row's lease forward. The worker calls this on a ticker so a sync
-- that legitimately runs longer than one lease is not swept out from under
-- itself. Guarded on state so a finished sync cannot be revived.
UPDATE channel_syncs
SET next_run_at = now() + interval '30 minutes'
WHERE id = $1 AND state = 'syncing';

-- name: SweepExpiredChannelSyncs :execrows
-- Return syncs whose lease elapsed while they were 'syncing' to the queue.
--
-- Replaces the boot-time blanket requeue of every syncing row, which a second
-- instance booting would have used to steal syncs the first was running. This
-- only touches rows whose owner has demonstrably stopped renewing.
--
-- channel_syncs has no attempt counter and no dead-lettering: a sync that keeps
-- failing simply retries on its next cadence, so 'idle' (not 'failed') is the
-- honest resting state for one that was interrupted rather than rejected.
UPDATE channel_syncs
SET state = 'idle', updated_at = now()
WHERE state = 'syncing' AND next_run_at <= now();
