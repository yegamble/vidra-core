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
-- The claim is wrapped in a CTE with an OUTER ORDER BY because UPDATE ...
-- RETURNING does not preserve the order of the subquery that chose the rows --
-- PostgreSQL returns them in whatever order it updated them. The "oldest first"
-- contract is real: these rows carry ordered side effects (an index mutation
-- applied out of order leaves the index stale; activities delivered out of order
-- are visible to the remote server), so the ordering has to be restated here.
--
-- It orders by created_at, NOT next_attempt_at: the claim overwrites
-- next_attempt_at with the lease, so every claimed row shares the same value by
-- the time the outer query runs. created_at is the stable proxy for "oldest".
--
-- created_at alone is NOT a total order, which is why id is the tiebreak. It
-- defaults to now(), and now() is TRANSACTION-fixed: every row enqueued inside
-- one transaction (a fan-out writes several) gets a byte-identical created_at,
-- so ORDER BY created_at leaves them tied and PostgreSQL is free to emit tied
-- rows in any order it likes -- including a different order on each claim. The
-- id tiebreak is what makes the sort deterministic. NOTE that id CANNOT carry
-- the ordering by itself here the way it does in search_outbox: that table's id
-- is a BIGSERIAL, but this one is a random uuid_generate_v4(), so ORDER BY id
-- would be a total order over an order that means nothing.
-- The inner selection gets the same id tiebreak so the LIMIT cut is
-- deterministic too: rows tied on next_attempt_at (again, same-transaction
-- enqueues) would otherwise be split across batches arbitrarily, and no amount
-- of outer ordering can repair an arbitrary split.
WITH claimed AS (
    UPDATE federation_deliveries
    SET next_attempt_at = now() + (sqlc.arg(lease_seconds)::int * interval '1 second'),
        updated_at = now()
    WHERE id IN (
        SELECT id FROM federation_deliveries
        WHERE state = 'pending' AND next_attempt_at <= now()
        ORDER BY next_attempt_at, id
        LIMIT sqlc.arg(batch_size)
        FOR UPDATE SKIP LOCKED
    )
    RETURNING id, inbox_url, payload, signing_channel_id, signing_channel_handle,
              signing_user_id, signing_username, attempts, created_at
)
SELECT id, inbox_url, payload, signing_channel_id, signing_channel_handle,
       signing_user_id, signing_username, attempts
FROM claimed
ORDER BY created_at, id;

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
