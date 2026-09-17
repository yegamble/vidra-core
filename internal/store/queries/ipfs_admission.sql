-- name: EnsureIPFSCapacity :one
INSERT INTO ipfs_capacity DEFAULT VALUES
ON CONFLICT (singleton) DO UPDATE SET singleton = true RETURNING *;

-- name: GetIPFSCapacity :one
SELECT * FROM ipfs_capacity WHERE singleton;

-- name: AdmitIPFSPin :one
-- Lock order is pin row -> singleton for admission AND release. The counter's
-- UPDATE rechecks its predicate against a concurrent committed version.
WITH candidate AS MATERIALIZED (
    SELECT p.object_key FROM media_ipfs_pins p
    WHERE p.object_key = sqlc.arg(object_key) AND p.network = 'public'
      AND p.state = 'pending' AND p.claim_token IS NULL
      AND p.next_attempt_at <= now() AND p.capacity_reason <> 'evicted_capacity'
    FOR UPDATE SKIP LOCKED
), policy AS MATERIALIZED (
    SELECT c.* FROM ipfs_control_config c
    WHERE c.singleton AND c.revision = sqlc.arg(config_revision)
      AND c.policy_active AND (c.config->>'enabled')::boolean
      AND c.config->>'provider' = 'internal' AND EXISTS (SELECT 1 FROM candidate)
    FOR SHARE
), budget AS (
    UPDATE ipfs_capacity b
    SET reserved_bytes = b.reserved_bytes + sqlc.arg(reservation_bytes)::bigint,
        active_claims = b.active_claims + 1
    FROM policy c
    WHERE b.singleton AND EXISTS (SELECT 1 FROM candidate)
      AND b.cleanup_pending = 0 AND b.maintenance_token IS NULL
      AND sqlc.arg(reservation_bytes)::bigint > 0
      AND sqlc.arg(repo_used_bytes)::bigint >= 0 AND sqlc.arg(filesystem_free_bytes)::bigint >= 0
      AND sqlc.arg(observed_at)::timestamptz >= b.measure_after
      AND sqlc.arg(observed_at)::timestamptz >= now() - interval '30 seconds'
      AND sqlc.arg(observed_at)::timestamptz <= now() + interval '5 seconds'
      AND b.active_claims < (c.config->>'workers')::integer
      AND sqlc.arg(repo_used_bytes)::bigint + b.reserved_bytes + sqlc.arg(reservation_bytes)::bigint <= (c.config->>'budget_bytes')::bigint
      AND sqlc.arg(filesystem_free_bytes)::bigint - b.reserved_bytes - sqlc.arg(reservation_bytes)::bigint >= (c.config->>'min_free_bytes')::bigint
    RETURNING b.singleton
)
UPDATE media_ipfs_pins p
SET claim_token = sqlc.arg(claim_token), lease_until = now() + interval '60 seconds',
    reservation_bytes = sqlc.arg(reservation_bytes), copied_bytes = 0,
    source_generation = sqlc.arg(source_generation), admitted_host_sequence = sqlc.arg(host_sequence),
    admitted_config_revision = sqlc.arg(config_revision),
    capacity_reason = '', next_attempt_at = now() + interval '60 seconds'
WHERE p.object_key IN (SELECT object_key FROM candidate) AND EXISTS (SELECT 1 FROM budget)
RETURNING p.*;

-- name: ReleaseIPFSReservation :execrows
-- The RPC must have returned (or a newer host restart been proven) before this
-- runs. A new measurement after release prevents reuse of an old free-space reading.
WITH claimed AS MATERIALIZED (
    SELECT p.object_key, p.reservation_bytes FROM media_ipfs_pins p
    WHERE p.object_key = sqlc.arg(object_key) AND p.claim_token = sqlc.arg(claim_token)
    FOR UPDATE
), released AS (
    UPDATE ipfs_capacity b
    SET reserved_bytes = b.reserved_bytes - claimed.reservation_bytes,
        active_claims = b.active_claims - 1, measure_after = now()
    FROM claimed WHERE b.singleton RETURNING claimed.object_key
)
UPDATE media_ipfs_pins p
SET claim_token = NULL, lease_until = NULL, reservation_bytes = 0,
    next_attempt_at = greatest(p.next_attempt_at, now())
WHERE p.object_key IN (SELECT object_key FROM released);

-- name: RenewIPFSReservation :execrows
UPDATE media_ipfs_pins
SET lease_until = now() + interval '60 seconds', next_attempt_at = now() + interval '60 seconds',
    copied_bytes = sqlc.arg(copied_bytes)
WHERE object_key = sqlc.arg(object_key) AND claim_token = sqlc.arg(claim_token)
  AND state = 'pending' AND network = 'public' AND lease_until > now()
  AND EXISTS (SELECT 1 FROM ipfs_control_config c WHERE c.singleton
      AND c.revision = admitted_config_revision AND (c.config->>'enabled')::boolean);

-- name: ListIPFSAdmissionCandidates :many
SELECT * FROM media_ipfs_pins
WHERE network = 'public' AND state = 'pending' AND claim_token IS NULL
  AND next_attempt_at <= now() AND capacity_reason <> 'evicted_capacity'
ORDER BY CASE policy_reason WHEN 'new' THEN 0 WHEN 'demand' THEN 1 WHEN 'backfill' THEN 2 ELSE 3 END,
    CASE WHEN media_class = 'hls' THEN 0 WHEN media_class = 'video_original' THEN 1 ELSE 2 END,
    demand_at DESC NULLS LAST, next_attempt_at
LIMIT sqlc.arg(batch_size);

-- name: DeferIPFSAdmission :exec
UPDATE media_ipfs_pins
SET capacity_reason = sqlc.arg(reason), next_attempt_at = now() + interval '60 seconds'
WHERE object_key = sqlc.arg(object_key) AND claim_token IS NULL AND state = 'pending';

-- name: ExpireIPFSReservation :exec
UPDATE media_ipfs_pins SET lease_until = now() - interval '1 second',
    attempts = attempts + 1, capacity_reason = 'copy_failed',
    state = CASE WHEN state = 'pending' AND attempts >= 5 THEN 'failed' ELSE state END
WHERE object_key = sqlc.arg(object_key) AND claim_token = sqlc.arg(claim_token);

-- name: ListExpiredIPFSReservations :many
SELECT * FROM media_ipfs_pins WHERE claim_token IS NOT NULL AND lease_until <= now()
ORDER BY lease_until LIMIT 100;

-- name: CompleteIPFSAdmission :one
-- Even a losing privacy/generation race records its CID for durable removal.
UPDATE media_ipfs_pins p
SET cid = sqlc.arg(cid), car_root = sqlc.arg(car_root), byte_size = sqlc.arg(byte_size),
    committed_generation = p.source_generation,
    state = CASE WHEN p.state = 'pending' AND p.network = 'public'
       AND NOT EXISTS (SELECT 1 FROM video_drm_keys k WHERE k.video_id = p.video_id)
       AND (p.media_class NOT IN ('hls','video_original','webm','thumbnail','storyboard','storyboard_vtt','caption') OR EXISTS (
           SELECT 1 FROM videos v JOIN channels ch ON ch.id=v.channel_id JOIN users u ON u.id=ch.owner_id
           WHERE v.id=p.video_id AND v.privacy='public' AND v.state='published' AND u.is_active AND NOT u.unlisted
             AND u.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM video_blocks b WHERE b.video_id=v.id)))
       AND (p.media_class <> 'hls' OR EXISTS (SELECT 1 FROM streaming_playlists sp
            WHERE sp.video_id = p.video_id AND sp.state = 'ready' AND sp.master_key = p.source_generation))
       THEN 'pinned' ELSE 'unpinning' END,
    last_error = '', updated_at = now()
WHERE p.object_key = sqlc.arg(object_key) AND p.claim_token = sqlc.arg(claim_token) AND p.lease_until > now()
RETURNING state;

-- name: TagIPFSPolicyIntent :exec
UPDATE media_ipfs_pins
SET policy_reason = CASE WHEN policy_reason = 'new' THEN 'new' ELSE sqlc.arg(reason)::text END,
    demand_at = CASE WHEN sqlc.arg(reason)::text = 'demand' THEN now() ELSE demand_at END,
    capacity_reason = '',
    state = CASE WHEN state IN ('unpinned', 'failed', 'unpinning') THEN 'pending' ELSE state END,
    next_attempt_at = CASE WHEN claim_token IS NULL THEN now() ELSE next_attempt_at END
WHERE object_key = sqlc.arg(object_key) AND network = 'public'
  AND (sqlc.arg(reason)::text <> 'demand' OR demand_at IS NULL OR demand_at < now() - interval '5 minutes');

-- name: ClaimIPFSRemovals :many
UPDATE media_ipfs_pins SET next_attempt_at = now() + interval '120 seconds'
WHERE object_key IN (
    SELECT object_key FROM media_ipfs_pins WHERE network = 'public' AND state = 'unpinning'
      AND claim_token IS NULL AND next_attempt_at <= now()
    ORDER BY next_attempt_at LIMIT sqlc.arg(batch_size) FOR UPDATE SKIP LOCKED
)
RETURNING object_key, media_class, cid, car_root, state, attempts, network, video_id, owner_user_id;

-- name: EvictColdIPFSPin :one
-- Only pins the managed policy owns are candidates. An unrelated/manual node
-- pin, legacy pin, shared root, or newly published/recently watched item is kept.
UPDATE media_ipfs_pins p
SET state = 'unpinning', capacity_reason = 'evicted_capacity', next_attempt_at = now(), updated_at = now()
WHERE p.object_key IN (
    SELECT candidate.object_key FROM media_ipfs_pins candidate
    WHERE candidate.network = 'public' AND candidate.state = 'pinned'
      AND candidate.claim_token IS NULL AND candidate.policy_reason <> 'legacy'
      AND candidate.created_at < now() - interval '1 hour'
      AND (candidate.demand_at IS NULL OR candidate.demand_at < now() - interval '1 hour')
      AND NOT EXISTS (SELECT 1 FROM media_ipfs_pins other WHERE other.cid = candidate.cid
          AND other.network = 'public' AND other.object_key <> candidate.object_key
          AND other.state IN ('pinned','pending','unpinning'))
    ORDER BY candidate.demand_at ASC NULLS FIRST, candidate.updated_at
    LIMIT 1 FOR UPDATE SKIP LOCKED
) RETURNING p.object_key;

-- name: IPFSAdmissionStats :one
SELECT
    count(*) FILTER (WHERE claim_token IS NOT NULL)::bigint AS copying,
    count(*) FILTER (WHERE state = 'pending' AND claim_token IS NULL AND policy_reason = 'new')::bigint AS queued_new,
    count(*) FILTER (WHERE state = 'pending' AND claim_token IS NULL AND policy_reason = 'demand')::bigint AS queued_demand,
    count(*) FILTER (WHERE state = 'pending' AND capacity_reason IN ('budget_exhausted','filesystem_headroom','oversized'))::bigint AS queued_capacity,
    count(*) FILTER (WHERE capacity_reason = 'evicted_capacity')::bigint AS evicted,
    count(*) FILTER (WHERE claim_token IS NOT NULL AND lease_until <= now())::bigint AS expired_claims,
    COALESCE(sum(copied_bytes) FILTER (WHERE claim_token IS NOT NULL),0)::bigint AS copied_bytes
FROM media_ipfs_pins WHERE network = 'public';

-- name: IPFSMediaProtected :one
SELECT EXISTS (SELECT 1 FROM video_drm_keys WHERE video_id = $1)::boolean;

-- name: SeedIPFSManagedBackfill :execrows
-- One bounded batch, only when the administrator explicitly enables old-media
-- backfill. Pick HLS when ready, otherwise the original, never both.
INSERT INTO media_ipfs_pins(object_key,media_class,video_id,policy_reason)
SELECT candidate.object_key,candidate.media_class,candidate.id,'backfill'
FROM (
    SELECT v.id,
        CASE WHEN sp.state='ready' AND sp.master_key<>'' THEN 'streaming-playlists/'||v.id::text||'/' ELSE vf.storage_key END AS object_key,
        CASE WHEN sp.state='ready' AND sp.master_key<>'' THEN 'hls' ELSE 'video_original' END AS media_class
    FROM videos v JOIN channels ch ON ch.id=v.channel_id JOIN users u ON u.id=ch.owner_id
    LEFT JOIN streaming_playlists sp ON sp.video_id=v.id
    LEFT JOIN video_files vf ON vf.video_id=v.id AND vf.kind='original'
    WHERE v.privacy='public' AND v.state='published' AND u.is_active AND NOT u.unlisted AND u.deleted_at IS NULL
      AND NOT EXISTS(SELECT 1 FROM video_blocks b WHERE b.video_id=v.id)
      AND NOT EXISTS(SELECT 1 FROM video_drm_keys k WHERE k.video_id=v.id)
) candidate
WHERE candidate.object_key IS NOT NULL AND candidate.object_key<>''
  AND NOT EXISTS(SELECT 1 FROM media_ipfs_pins p WHERE p.object_key=candidate.object_key)
  AND EXISTS(SELECT 1 FROM ipfs_control_config c WHERE c.singleton AND c.policy_active
      AND (c.config->>'enabled')::boolean AND (c.config->>'backfill_enabled')::boolean)
ORDER BY candidate.id LIMIT 20
ON CONFLICT(object_key) DO NOTHING;
