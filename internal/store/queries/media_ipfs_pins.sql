-- IPFS mirror pin ledger + durable pin/unpin queue (migration 0071, fix_plan
-- P19.1, .ralph/specs/ipfs-media.md). The mirror is a SIDECAR: these queries only
-- ever touch already-public objects (the eligibility gate lives in the enqueue
-- helper, P19.2). states: pending → pinned | failed, and unpinning → unpinned for
-- the delete/GC path. Keyed by object_key (the authoritative storage.Backend key).

-- name: UpsertIPFSPinIntent :one
-- Enqueue (or re-arm) a pin intent for one storage object. Keyed on object_key so
-- a class that changes its key (e.g. re-uploaded avatar with a new extension)
-- lands as a new row and the old key is unpinned separately. A previously-terminal
-- row (failed / unpinned / mid-unpin) is re-armed to pending; a live pinned/pending
-- row is left as-is (idempotent: re-enqueue of an unchanged live pin is a no-op).
INSERT INTO media_ipfs_pins (object_key, media_class, video_id, owner_user_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (object_key) DO UPDATE
SET media_class   = EXCLUDED.media_class,
    video_id      = EXCLUDED.video_id,
    owner_user_id = EXCLUDED.owner_user_id,
    state = CASE
        WHEN media_ipfs_pins.state IN ('failed', 'unpinned', 'unpinning') THEN 'pending'
        ELSE media_ipfs_pins.state
    END,
    attempts = CASE
        WHEN media_ipfs_pins.state IN ('failed', 'unpinned', 'unpinning') THEN 0
        ELSE media_ipfs_pins.attempts
    END,
    next_attempt_at = CASE
        WHEN media_ipfs_pins.state IN ('failed', 'unpinned', 'unpinning') THEN now()
        ELSE media_ipfs_pins.next_attempt_at
    END,
    last_error = CASE
        WHEN media_ipfs_pins.state IN ('failed', 'unpinned', 'unpinning') THEN ''
        ELSE media_ipfs_pins.last_error
    END,
    updated_at = now()
RETURNING *;

-- name: EnqueueIPFSUnpin :exec
-- Flip a live pin toward removal: the delete/GC path (P19.3+) sets an existing
-- row to 'unpinning' so ClaimDue leases it to the worker. Only pinned/pending rows
-- transition (a terminal or already-unpinning row is left alone).
UPDATE media_ipfs_pins
SET state = 'unpinning', attempts = 0, next_attempt_at = now(), last_error = '', updated_at = now()
WHERE object_key = $1 AND state IN ('pinned', 'pending');

-- name: ClaimDueIPFSPins :many
-- Atomically leases due pin/unpin work for a worker. FOR UPDATE SKIP LOCKED lets
-- IPFS_PIN_CONCURRENCY workers claim disjoint rows without blocking each other;
-- pushing next_attempt_at forward by the lease seconds ($2) is the visibility
-- timeout (a crashed worker's row is retried after the lease; a concurrent claimer
-- skips it meanwhile). state is PRESERVED — pending ⇒ add+pin, unpinning ⇒ unpin —
-- and the worker sets the terminal state via MarkPinned / MarkFailed / MarkUnpinned
-- (pinning and unpinning are idempotent, so an over-long lease is safe).
UPDATE media_ipfs_pins
SET next_attempt_at = now() + (sqlc.arg(lease_seconds)::int * interval '1 second'),
    updated_at = now()
WHERE object_key IN (
    SELECT object_key FROM media_ipfs_pins
    WHERE state IN ('pending', 'unpinning') AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
RETURNING object_key, media_class, cid, car_root, state, attempts, video_id, owner_user_id;

-- name: MarkIPFSPinned :exec
-- Terminal success for a pin: record the CID (and wrap-directory root for HLS
-- trees) and the pinned byte size.
UPDATE media_ipfs_pins
SET state = 'pinned', cid = $2, car_root = $3, byte_size = $4, last_error = '', updated_at = now()
WHERE object_key = $1;

-- name: MarkIPFSPinFailed :exec
-- Dead-letter a pin/unpin after max attempts: no further automatic retries (the
-- reconciliation scan can re-arm it later). last_error is a SAFE, client-invisible
-- reason — never the raw node error verbatim.
UPDATE media_ipfs_pins
SET state = 'failed', attempts = attempts + 1, last_error = $2, updated_at = now()
WHERE object_key = $1;

-- name: RescheduleIPFSPin :exec
-- Transient retry with backoff: leave the row in its current actionable state
-- (pending or unpinning) and push next_attempt_at out so a later drain retries it.
UPDATE media_ipfs_pins
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE object_key = $1;

-- name: MarkIPFSPinUnpinned :exec
-- Terminal success for an unpin (the delete/GC path). The row is kept (not
-- deleted) as an audit trail; content-address dedupe (P19.3) reads it.
UPDATE media_ipfs_pins
SET state = 'unpinned', last_error = '', updated_at = now()
WHERE object_key = $1;

-- name: CountIPFSPinsByStateClass :many
-- Admin status aggregation (P19.2 /ipfs/status): pin counts per state and media
-- class, cheap off media_ipfs_pins_state_class_idx.
SELECT state, media_class, count(*)::bigint AS count
FROM media_ipfs_pins
GROUP BY state, media_class
ORDER BY state, media_class;

-- name: CountIPFSPinsSharingCID :one
-- Reference count for content-address dedupe (P19.3 "never unpin without
-- reference checking"): how many OTHER live rows (pinned/pending) share this CID.
-- The worker only issues the network unpin when this is zero.
SELECT count(*)::bigint
FROM media_ipfs_pins
WHERE cid = $1 AND cid <> '' AND object_key <> $2 AND state IN ('pinned', 'pending');

-- name: GetIPFSPinByObjectKey :one
SELECT * FROM media_ipfs_pins WHERE object_key = $1;

-- name: ListIPFSPinsByVideo :many
-- Every ledger row for a video (privacy re-evaluation + cascade unpin, P19.3),
-- off media_ipfs_pins_video_idx.
SELECT * FROM media_ipfs_pins WHERE video_id = $1 ORDER BY media_class, object_key;
