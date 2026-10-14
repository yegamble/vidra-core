-- PeerTube export "following" import queue (migration 0180).

-- name: CreatePeerTubeFollowImport :one
INSERT INTO peertube_follow_imports (user_id) VALUES ($1) RETURNING id;

-- name: AddPeerTubeFollowImportTargets :execrows
-- DO NOTHING covers both unique indexes: a target already pending for this user
-- is not queued again.
INSERT INTO peertube_follow_import_targets (import_id, user_id, target)
SELECT $1, $2, t FROM unnest($3::text[]) AS t
ON CONFLICT DO NOTHING;

-- name: CountPendingPeerTubeFollowTargets :one
SELECT count(*)::bigint FROM peertube_follow_import_targets WHERE user_id = $1 AND state = 'pending';

-- name: ListFollowedRemoteHandles :many
-- name@domain (lower-case) of every remote channel the user already follows.
SELECT lower(ra.preferred_username || '@' || ra.domain)::text AS handle
FROM remote_channel_follows f
JOIN remote_actors ra ON ra.actor_url = f.remote_actor_url
WHERE f.user_id = $1;

-- name: PrunePeerTubeFollowImports :execrows
-- Finished imports (no pending target) older than the cutoff; targets cascade.
DELETE FROM peertube_follow_imports
WHERE id IN (
    SELECT j.id FROM peertube_follow_imports j
    WHERE j.created_at < $1
      AND NOT EXISTS (SELECT 1 FROM peertube_follow_import_targets t WHERE t.import_id = j.id AND t.state = 'pending')
    ORDER BY j.created_at LIMIT 500
);

-- name: ClaimDuePeerTubeFollowTargets :many
-- Leases due targets by pushing next_attempt_at forward, so a worker killed
-- mid-target releases it when the lease passes. Round-robin: each user
-- contributes at most per_user_limit rows, ranked by age, and rows are taken
-- rank by rank, so one user's backlog cannot starve another's. SKIP LOCKED:
-- concurrent claimers take disjoint rows.
UPDATE peertube_follow_import_targets
SET next_attempt_at = now() + make_interval(secs => sqlc.arg('lease_seconds')::double precision),
    updated_at = now()
WHERE id IN (
    SELECT t.id FROM peertube_follow_import_targets t
    JOIN (
        SELECT id, row_number() OVER (PARTITION BY user_id ORDER BY next_attempt_at, id) AS rn
        FROM peertube_follow_import_targets
        WHERE state = 'pending' AND next_attempt_at <= now()
    ) due ON due.id = t.id
    WHERE due.rn <= sqlc.arg('per_user_limit')::int
    ORDER BY due.rn, t.next_attempt_at, t.id
    LIMIT sqlc.arg('claim_limit')
    FOR UPDATE OF t SKIP LOCKED
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
