-- Outbound federation delivery queue (migration 0038, .ralph/specs/federation.md §8).

-- name: EnqueueDelivery :exec
-- Exactly one signer is set per row: the channel columns (a channel actor
-- signs, e.g. video fan-out) or the user columns (the user's ACCOUNT actor
-- signs, e.g. an outbound remote-channel Follow/Undo — migration 0052).
INSERT INTO federation_deliveries (
    inbox_url, payload, signing_channel_id, signing_channel_handle,
    signing_user_id, signing_username
)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ClaimDueDeliveries :many
-- LEASES pending deliveries whose backoff has elapsed, oldest first.
--
-- This was a bare SELECT with a comment saying a single worker drains
-- sequentially so no locking was needed. That made it the worst of the queues to
-- run on two nodes: nothing marked the row as taken, so both nodes would read it
-- and both would POST the activity. Duplicate federation delivery is not an
-- internal inefficiency -- it is visible to every remote server that receives it.
-- The lease is next_attempt_at pushed forward: a claimed row stops being due for
-- lease_seconds, so a second worker's claim skips it and a CRASHED worker's row
-- becomes due again by itself. FOR UPDATE SKIP LOCKED makes concurrent claimers
-- take disjoint rows without blocking each other.
UPDATE federation_deliveries
SET next_attempt_at = now() + (sqlc.arg(lease_seconds)::int * interval '1 second'),
    updated_at = now()
WHERE id IN (
    SELECT id FROM federation_deliveries
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
RETURNING id, inbox_url, payload, signing_channel_id, signing_channel_handle,
          signing_user_id, signing_username, attempts;

-- name: MarkDeliveryDelivered :exec
UPDATE federation_deliveries
SET state = 'delivered', updated_at = now()
WHERE id = $1;

-- name: RescheduleDelivery :exec
UPDATE federation_deliveries
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE id = $1;

-- name: FailDelivery :exec
UPDATE federation_deliveries
SET state = 'failed', attempts = attempts + 1, last_error = $2, updated_at = now()
WHERE id = $1;
