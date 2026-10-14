-- PeerTube export "following" import queue (migration 0180).

-- name: CreatePeerTubeFollowImport :one
INSERT INTO peertube_follow_imports (user_id) VALUES ($1) RETURNING id;

-- name: AddPeerTubeFollowImportTargets :exec
INSERT INTO peertube_follow_import_targets (import_id, user_id, target)
SELECT $1, $2, t FROM unnest($3::text[]) AS t
ON CONFLICT (import_id, target) DO NOTHING;

-- name: ClaimDuePeerTubeFollowTargets :many
-- Leases due targets by pushing next_attempt_at forward, so a worker killed
-- mid-target releases it when the lease passes. SKIP LOCKED: concurrent
-- claimers take disjoint rows.
UPDATE peertube_follow_import_targets
SET next_attempt_at = now() + make_interval(secs => sqlc.arg('lease_seconds')::double precision),
    updated_at = now()
WHERE id IN (
    SELECT id FROM peertube_follow_import_targets
    WHERE state = 'pending' AND next_attempt_at <= now()
    ORDER BY next_attempt_at, id
    LIMIT sqlc.arg('claim_limit')
    FOR UPDATE SKIP LOCKED
)
RETURNING id, user_id, target, attempts;

-- name: FinishPeerTubeFollowTarget :exec
UPDATE peertube_follow_import_targets
SET state = $2, last_error = $3, attempts = attempts + 1, updated_at = now()
WHERE id = $1;

-- name: ReschedulePeerTubeFollowTarget :exec
UPDATE peertube_follow_import_targets
SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
WHERE id = $1;

-- name: CountPeerTubeFollowImportTargets :many
-- Status of ONE import the caller owns: counts per state. No row for an
-- unknown id or another user's, which the handler answers as 404.
SELECT t.state, count(*)::bigint AS n
FROM peertube_follow_import_targets t
JOIN peertube_follow_imports j ON j.id = t.import_id
WHERE j.id = $1 AND j.user_id = $2
GROUP BY t.state;

-- name: GetPeerTubeFollowImport :one
SELECT id, created_at FROM peertube_follow_imports WHERE id = $1 AND user_id = $2;
