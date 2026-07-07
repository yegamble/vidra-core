-- Durable per-user IPFS mirror re-evaluation queue (migration 0072, fix_plan P19
-- round-2 audit MAJOR). The unlisted-toggle / activation-change re-evaluation is a
-- fan-out over every one of an owner's videos + identity images; the HTTP handler
-- enqueues ONE compact job and the mirror worker expands it off the request path
-- with per-video error isolation.

-- name: EnqueueIPFSUserReeval :exec
-- Enqueue (or re-arm) a durable per-user mirror re-evaluation. Idempotent: a rapid
-- sequence of toggles coalesces onto ONE due job (ON CONFLICT resets it to due now,
-- clearing any backoff) so the worker re-evaluates once against the LATEST committed
-- flags. The handler calls this instead of running the fan-out inline.
INSERT INTO media_ipfs_user_reevals (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO UPDATE
SET attempts = 0, next_attempt_at = now(), last_error = '', updated_at = now();

-- name: ClaimDueIPFSUserReevals :many
-- Lease due re-eval jobs for the worker. FOR UPDATE SKIP LOCKED + a lease
-- (next_attempt_at pushed forward by the lease seconds) mirror the pin queue's claim:
-- concurrent workers claim disjoint jobs without blocking, and a crashed worker's
-- lease is retried once it expires. Re-evaluation is idempotent (upsert/enqueue-unpin
-- ledger writes), so an over-long lease is safe.
UPDATE media_ipfs_user_reevals
SET next_attempt_at = now() + (sqlc.arg(lease_seconds)::int * interval '1 second'),
    updated_at = now()
WHERE user_id IN (
    SELECT user_id FROM media_ipfs_user_reevals
    WHERE next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
RETURNING user_id, attempts;

-- name: DeleteIPFSUserReeval :exec
-- Terminal success: the worker fully re-evaluated the user (every video + image
-- routed through the eligibility fence and enqueued), so drop the job.
DELETE FROM media_ipfs_user_reevals WHERE user_id = $1;

-- name: RescheduleIPFSUserReeval :exec
-- Transient partial failure: at least one video/image failed to re-evaluate, so keep
-- the job and retry with backoff. The items that DID succeed are already durably
-- enqueued (the ledger writes are idempotent), so the retry only re-does the
-- stragglers. Deliberately never dead-letters — a stranded re-eval is a privacy
-- concern, and the periodic eligibility sweep is the belt-and-suspenders backstop.
UPDATE media_ipfs_user_reevals
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE user_id = $1;
