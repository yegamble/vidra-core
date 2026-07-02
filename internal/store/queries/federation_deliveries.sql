-- Outbound federation delivery queue (migration 0038, .ralph/specs/federation.md §8).

-- name: EnqueueDelivery :exec
INSERT INTO federation_deliveries (inbox_url, payload, signing_channel_id, signing_channel_handle)
VALUES ($1, $2, $3, $4);

-- name: ClaimDueDeliveries :many
-- Pending deliveries whose backoff has elapsed, oldest first. A single worker
-- drains sequentially, so no row-locking is needed yet.
SELECT id, inbox_url, payload, signing_channel_id, signing_channel_handle, attempts
FROM federation_deliveries
WHERE state = 'pending' AND next_attempt_at <= now()
ORDER BY next_attempt_at
LIMIT $1;

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
