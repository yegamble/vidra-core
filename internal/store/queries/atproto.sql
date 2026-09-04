-- ATProto / Bluesky outbound cross-posting (migration 0066, .ralph/specs/atproto.md).

-- name: UpsertATProtoAccount :one
-- Link (or re-link) the caller's Bluesky account. app_password_sealed is the
-- secretbox-sealed app password (never the plaintext). Re-linking overwrites the
-- handle/did/pds/password/auto_post; last_posted_at and created_at are preserved.
INSERT INTO atproto_accounts (
    user_id, handle, did, pds_url, app_password_sealed, auto_post
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) DO UPDATE SET
    handle              = EXCLUDED.handle,
    did                 = EXCLUDED.did,
    pds_url             = EXCLUDED.pds_url,
    app_password_sealed = EXCLUDED.app_password_sealed,
    auto_post           = EXCLUDED.auto_post
RETURNING *;

-- name: GetATProtoAccount :one
SELECT * FROM atproto_accounts WHERE user_id = $1;

-- name: DeleteATProtoAccount :exec
DELETE FROM atproto_accounts WHERE user_id = $1;

-- name: TouchATProtoAccountPosted :exec
UPDATE atproto_accounts SET last_posted_at = now() WHERE user_id = $1;

-- name: EnqueueATProtoPost :exec
-- Enqueue at most one auto-post per video (video_id UNIQUE): a repeat publish
-- transition is a no-op.
INSERT INTO atproto_posts (video_id)
VALUES ($1)
ON CONFLICT (video_id) DO NOTHING;

-- name: ClaimDueATProtoPosts :many
-- LEASES pending cross-posts whose backoff has elapsed, oldest first.
--
-- Previously a bare SELECT: on two nodes both would read the same row and both
-- would create a Bluesky post, so one video became two posts on the user's
-- public feed. Externally visible and not undoable by a retry.
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
-- one transaction (a bulk publish queues several) gets a byte-identical
-- created_at, so ORDER BY created_at leaves them tied and PostgreSQL is free to
-- emit tied rows in any order it likes -- including a different order on each
-- claim. The id tiebreak is what makes the sort deterministic. NOTE that id
-- CANNOT carry the ordering by itself here the way it does in search_outbox:
-- that table's id is a BIGSERIAL, but this one is a random uuid_generate_v4(),
-- so ORDER BY id would be a total order over an order that means nothing.
-- The inner selection gets the same id tiebreak so the LIMIT cut is
-- deterministic too: rows tied on next_attempt_at (again, same-transaction
-- enqueues) would otherwise be split across batches arbitrarily, and no amount
-- of outer ordering can repair an arbitrary split.
WITH claimed AS (
    UPDATE atproto_posts
    SET next_attempt_at = now() + (sqlc.arg(lease_seconds)::int * interval '1 second'),
        updated_at = now()
    WHERE id IN (
        SELECT id FROM atproto_posts
        WHERE state = 'pending' AND next_attempt_at <= now()
        ORDER BY next_attempt_at, id
        LIMIT sqlc.arg(batch_size)
        FOR UPDATE SKIP LOCKED
    )
    RETURNING id, video_id, attempts, created_at
)
SELECT id, video_id, attempts
FROM claimed
ORDER BY created_at, id;

-- name: MarkATProtoPostDone :exec
UPDATE atproto_posts
SET state = 'posted', post_uri = $2, updated_at = now()
WHERE id = $1;

-- name: RescheduleATProtoPost :exec
UPDATE atproto_posts
SET attempts = attempts + 1, next_attempt_at = $2, error = $3, updated_at = now()
WHERE id = $1;

-- name: FailATProtoPost :exec
UPDATE atproto_posts
SET state = 'failed', attempts = attempts + 1, error = $2, updated_at = now()
WHERE id = $1;

-- name: GetATProtoPostByVideo :one
-- Read a video's queued post row (tests / status).
SELECT * FROM atproto_posts WHERE video_id = $1;

-- name: GetATProtoPostVideo :one
-- Resolve the video's owner + title + privacy for the enqueue decision and for
-- building the post record. Joins through the owning channel so the atproto
-- package needs no dependency on the video package. atproto_enabled gates the
-- per-channel cross-post opt-out (migration 0096).
SELECT v.id, v.title, v.privacy, v.short_code, c.owner_id, c.atproto_enabled
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE v.id = $1;
