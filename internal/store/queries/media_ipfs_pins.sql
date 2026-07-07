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

-- name: BackfillIPFSPinIntent :execrows
-- One-shot catalog backfill (P19.6 admin reconcile): seed a pin intent for a
-- pre-existing eligible object that has NO ledger row yet. Unlike
-- UpsertIPFSPinIntent (which re-arms a terminal row) this NEVER touches an existing
-- row in any state — the periodic reconcile re-arms failures; the backfill's sole
-- job is to seed rows for objects that predate the mirror (or were missed during an
-- outage). ON CONFLICT DO NOTHING makes it idempotent, and :execrows returns 1 when
-- a row was inserted, 0 when one already existed, so the caller tallies the
-- newly-enqueued objects per class and a second run provably enqueues zero. The
-- eligibility gate is enforced by the caller BEFORE this runs (the privacy fence);
-- only already-public objects ever reach here.
INSERT INTO media_ipfs_pins (object_key, media_class, video_id, owner_user_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (object_key) DO NOTHING;

-- name: RepinIPFSObject :exec
-- Force-(re)arm a pin intent for a wholesale-replaced transcode output (P19.4).
-- The HLS tree (object_key streaming-playlists/<id>/, trailing slash = directory
-- intent) and the VP9/WebM alternate keep a STABLE object_key across re-transcodes
-- while their content — and therefore their CID — is replaced in full ('0039'
-- wholesale-replace). Unlike UpsertIPFSPinIntent (which leaves a live 'pinned' row
-- untouched, correct for stable content), this ALWAYS moves the row back to
-- 'pending' so the worker re-adds the new tree and swaps the CID. The existing
-- cid/car_root is PRESERVED (not reset) so the worker can reference-check and unpin
-- the superseded root after the new add succeeds. A brand-new object_key is
-- inserted 'pending'. Called ONLY from the transcode-completion hook for an
-- ELIGIBLE (public+published) video — the privacy gate is re-checked there, so a
-- private/deleted video never reaches this and no non-public tree is ever armed.
INSERT INTO media_ipfs_pins (object_key, media_class, video_id)
VALUES ($1, $2, $3)
ON CONFLICT (object_key) DO UPDATE
SET media_class     = EXCLUDED.media_class,
    video_id        = EXCLUDED.video_id,
    state           = 'pending',
    attempts        = 0,
    next_attempt_at = now(),
    last_error      = '',
    updated_at      = now();

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

-- name: MarkIPFSPinned :one
-- Terminal success for a pin: record the CID (and wrap-directory root for HLS
-- trees) and the pinned byte size. STATE-GUARDED against a lost-update race with a
-- concurrent privacy flip (P19 audit BLOCKER): the CID/car_root/byte_size are
-- ALWAYS persisted — the bytes ARE on the node now, so a subsequent unpin needs the
-- CID to target them — but state advances to 'pinned' ONLY when the row is still
-- pinnable (pending, or idempotently pinned). If a concurrent EnqueueIPFSUnpin
-- flipped the leased row to 'unpinning' (the object went non-public mid-Add), the
-- CASE KEEPS 'unpinning' so the unpin worker removes the now-non-public bytes: the
-- stale pin worker NEVER clobbers the newer visibility fact back to 'pinned'.
-- RETURNING state lets the worker learn which outcome happened (pinned vs raced)
-- and, on a loss, execute the unpin immediately instead of leaking until the next
-- reconcile.
UPDATE media_ipfs_pins
SET cid = $2,
    car_root = $3,
    byte_size = $4,
    state = CASE WHEN state IN ('pending', 'pinned') THEN 'pinned' ELSE state END,
    last_error = '',
    updated_at = now()
WHERE object_key = $1
RETURNING state;

-- name: MarkIPFSPinFailed :exec
-- Dead-letter a pin/unpin after max attempts: no further automatic retries (the
-- reconciliation scan can re-arm it later). last_error is a SAFE, client-invisible
-- reason — never the raw node error verbatim. STATE-GUARDED (P19 audit): the
-- dead-letter only lands when the row is STILL in the actionable state the worker
-- claimed (sqlc.arg(expected_state) — 'pending' for a pin, 'unpinning' for an
-- unpin). If a concurrent privacy flip re-directed the row (pending↔unpinning), the
-- newer intent is preserved rather than clobbered to 'failed' — which the reconcile
-- scan would otherwise re-arm in the WRONG direction (a failed-pin cid='' re-pins,
-- so a clobbered unpin would re-pin a now-non-public object: a privacy regression).
UPDATE media_ipfs_pins
SET state = 'failed', attempts = attempts + 1, last_error = $2, updated_at = now()
WHERE object_key = $1 AND state = sqlc.arg(expected_state);

-- name: RescheduleIPFSPin :exec
-- Transient retry with backoff: leave the row in WHATEVER actionable state it is
-- currently in (pending or unpinning) and push next_attempt_at out so a later drain
-- retries it. Deliberately state-agnostic — it never advances to a terminal state,
-- so a concurrent privacy flip that re-directed the row (pending↔unpinning) is
-- preserved and simply retried in the newer direction; a failed pin never recorded
-- a CID, so a re-directed unpin of it is a harmless node no-op.
UPDATE media_ipfs_pins
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE object_key = $1;

-- name: MarkIPFSPinUnpinned :exec
-- Terminal success for an unpin (the delete/GC path). The row is kept (not
-- deleted) as an audit trail; content-address dedupe (P19.3) reads it. STATE-GUARDED
-- (P19 audit): only completes the unpin when the row is STILL 'unpinning'. If a
-- concurrent private→public flip re-armed it to 'pending' (UpsertIPFSPinIntent)
-- while the node unpin was in flight, that newer pin intent is preserved (the worker
-- re-pins the now-public object) rather than clobbered to 'unpinned' — the
-- double-flip converges to the latest visibility fact.
UPDATE media_ipfs_pins
SET state = 'unpinned', last_error = '', updated_at = now()
WHERE object_key = $1 AND state = 'unpinning';

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

-- name: RearmFailedIPFSPins :execrows
-- Reconciliation re-arm (P19.2): move dead-lettered rows back to an actionable
-- state so the worker retries them, bounded per scan. The discriminator is the
-- CID: a failed PIN never recorded a CID (cid = '') so it re-arms to 'pending'
-- (add+pin again); a failed UNPIN was previously pinned (cid <> '') so it re-arms
-- to 'unpinning' (retry the removal — dropping an unpin would be a privacy
-- regression). next_attempt_at = now() makes them immediately claimable.
UPDATE media_ipfs_pins
SET state = CASE WHEN cid = '' THEN 'pending' ELSE 'unpinning' END,
    attempts = 0, next_attempt_at = now(), last_error = '', updated_at = now()
WHERE object_key IN (
    SELECT object_key FROM media_ipfs_pins
    WHERE state = 'failed'
    ORDER BY updated_at
    LIMIT sqlc.arg(batch_size)
);

-- name: GetIPFSPinByObjectKey :one
SELECT * FROM media_ipfs_pins WHERE object_key = $1;

-- name: ListIPFSPinsByVideo :many
-- Every ledger row for a video (privacy re-evaluation + cascade unpin, P19.3),
-- off media_ipfs_pins_video_idx.
SELECT * FROM media_ipfs_pins WHERE video_id = $1 ORDER BY media_class, object_key;

-- name: ListPinnedVideoIDs :many
-- Feed/card badge batch lookup (P19.3): of the given video ids, which have at
-- least one currently-pinned ledger row. One indexed scan for a whole page (the
-- ipfs_pinned boolean), off media_ipfs_pins_video_idx, so the badge never costs
-- a per-card query. Only 'pinned' rows count — a video whose media was unpinned
-- (went private) is correctly reported as not pinned.
SELECT DISTINCT video_id
FROM media_ipfs_pins
WHERE state = 'pinned'
  AND video_id IS NOT NULL
  AND video_id = ANY(sqlc.arg('video_ids')::uuid[]);
