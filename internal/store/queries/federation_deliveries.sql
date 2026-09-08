-- Outbound federation delivery queue (migration 0038, .ralph/specs/federation.md §8).

-- name: EnqueueDelivery :exec
-- Exactly one signer is set per row: the channel columns (a channel actor
-- signs, e.g. video fan-out) or the user columns (the user's ACCOUNT actor
-- signs, e.g. an outbound remote-channel Follow/Undo — migration 0052).
--
-- request_id/correlation_id come from the REQUEST that produced the fan-out
-- (migration 0139), so twelve deliveries from one publish are recognisably one
-- act — and so a stuck inbox can be traced back to what queued for it.
INSERT INTO federation_deliveries (
    inbox_url, payload, signing_channel_id, signing_channel_handle,
    signing_user_id, signing_username, request_id, correlation_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

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

-- name: ListCancelledDeliveriesForRedelivery :many
-- Deliveries this instance CANCELLED — never sent — because their destination
-- was on the admin blocklist, since a given moment.
--
-- A29 measured the gap this closes: a block cancels outbound activities as well
-- as refusing inbound ones, and lifting the block resumed nothing. The refused
-- INBOUND activities are gone for good (the remote was answered 202 and will
-- not resend), but this instance's own outbound rows are still here with their
-- payloads intact, so the half that CAN be repaired is repaired.
--
-- The cancel marker is passed in rather than spelled here so the string lives
-- once, next to the code that writes it (internal/federation's
-- deliveryCancelledBlocked). `since` is the moment the block began, which is
-- what bounds this to the block's own window: a delivery that failed for any
-- other reason, or was cancelled by an EARLIER block that was already lifted,
-- is not this unblock's to resume.
--
-- Rows are capped by the caller; a block window with more cancellations than
-- the cap leaves the remainder where they are rather than unbounding an admin
-- request.
--
-- host_like is a PREFILTER, not the answer. The caller decides which rows
-- belong to the unblocked domain with the same hostOf() that decided to cancel
-- them, so the two cannot disagree about what host an inbox URL has; this
-- clause only keeps the LIMIT from being spent on OTHER domains' cancelled
-- rows. Without it, an instance with three blocked domains and more than
-- `result_limit` cancellations in the window could unblock one domain and
-- resume none of its deliveries, because the page came back full of the two
-- that are still blocked. It can only ever widen the candidate set relative to
-- the real answer: hostOf(inbox_url) = <domain> implies the domain appears
-- literally in the URL, and LIKE's own metacharacters in a hostname (an
-- underscore) match more rather than fewer.
SELECT id, inbox_url, payload
FROM federation_deliveries
WHERE state = 'failed'
  AND last_error = sqlc.arg(cancel_reason)
  AND updated_at >= sqlc.arg(since)
  AND inbox_url ILIKE '%' || sqlc.arg(host_like)::text || '%'
ORDER BY created_at, id
LIMIT sqlc.arg(result_limit);

-- name: RequeueCancelledDelivery :execrows
-- Put ONE cancelled delivery back on the queue: pending, due now, attempts
-- reset, the cancellation note cleared.
--
-- Attempts are reset because a cancellation is not an attempt — the activity
-- was never sent, so the row's attempt budget was never spent on the remote
-- side, and carrying the cancelled row's count forward would dead-letter it
-- early for reasons that have nothing to do with the destination's health.
--
-- The state guard makes this idempotent under a double unblock: the second
-- caller matches nothing because the first already moved the row to pending.
UPDATE federation_deliveries
SET state = 'pending', attempts = 0, next_attempt_at = now(),
    last_error = '', updated_at = now()
WHERE id = $1 AND state = 'failed';
