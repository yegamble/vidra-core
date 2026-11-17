-- The queue of remote outboxes to read (migration 0190).

-- name: EnqueueOutboxCrawl :exec
-- Queues one read of a followed actor's outbox. Idempotent: an actor already
-- queued stays as it is, and one whose read finished (or was given up on)
-- inside the cooldown is not queued again.
INSERT INTO federation_outbox_crawls (actor_url)
VALUES (sqlc.arg('actor_url'))
ON CONFLICT (actor_url) DO UPDATE
SET state = 'pending', attempts = 0, next_attempt_at = now(), last_error = '',
    queued_at = now(), updated_at = now()
WHERE federation_outbox_crawls.state <> 'pending'
  AND federation_outbox_crawls.updated_at <= now() - (sqlc.arg('cooldown_seconds')::int * interval '1 second');

-- name: ClaimDueOutboxCrawls :many
-- LEASES due reads, oldest first. A claimed row's next_attempt_at moves forward
-- by the lease, so a second worker skips it and a crashed worker's row comes
-- due again by itself; FOR UPDATE SKIP LOCKED gives concurrent claimers
-- disjoint rows. Same shape, for the same reasons, as ClaimDueDeliveries.
WITH claimed AS (
    UPDATE federation_outbox_crawls
    SET next_attempt_at = now() + (sqlc.arg('lease_seconds')::int * interval '1 second'),
        updated_at = now()
    WHERE actor_url IN (
        SELECT actor_url FROM federation_outbox_crawls
        WHERE state = 'pending' AND next_attempt_at <= now()
        ORDER BY next_attempt_at, actor_url
        LIMIT sqlc.arg('batch_size')
        FOR UPDATE SKIP LOCKED
    )
    RETURNING actor_url, attempts, queued_at
)
SELECT actor_url, attempts FROM claimed ORDER BY queued_at, actor_url;

-- name: SettleOutboxCrawl :exec
-- Records the outcome of one attempt: 'done', 'failed' (given up; last_error
-- says why), or 'pending' again with the time of the next try.
UPDATE federation_outbox_crawls
SET state = sqlc.arg('state'), attempts = attempts + 1,
    next_attempt_at = sqlc.arg('next_attempt_at'), last_error = sqlc.arg('last_error'),
    updated_at = now()
WHERE actor_url = sqlc.arg('actor_url');
