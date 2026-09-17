-- name: RecordIPFSReturnedCopy :exec
-- The counter shares admission's UPDATE lock: a cross-table NOT EXISTS alone
-- can admit a copy from an older READ COMMITTED statement snapshot.
WITH recorded AS (
    INSERT INTO ipfs_copy_cleanup(claim_token, cid, object_key)
    VALUES(sqlc.arg(claim_token), sqlc.arg(cid), sqlc.arg(object_key))
    ON CONFLICT DO NOTHING RETURNING cid
)
INSERT INTO ipfs_capacity(cleanup_pending)
SELECT count(*) FROM recorded HAVING count(*) > 0
ON CONFLICT (singleton) DO UPDATE
SET cleanup_pending = ipfs_capacity.cleanup_pending + EXCLUDED.cleanup_pending;

-- name: BeginIPFSCopyCleanup :one
UPDATE ipfs_capacity
SET maintenance_token = sqlc.arg(maintenance_token), maintenance_until = now() + interval '12 minutes',
    maintenance_host_sequence = sqlc.arg(host_sequence), maintenance_config_revision = sqlc.arg(config_revision)
WHERE singleton AND active_claims = 0 AND cleanup_pending > 0 AND maintenance_token IS NULL
RETURNING *;

-- name: NextIPFSCopyCleanup :one
SELECT * FROM ipfs_copy_cleanup ORDER BY created_at, claim_token, cid LIMIT 1;

-- name: IPFSReturnedRootReferenced :one
-- A live ledger row owns withdrawal. Sharing also protects a newer claim for
-- the same object; object_key inequality would mistakenly unpin that new root.
SELECT EXISTS (SELECT 1 FROM media_ipfs_pins WHERE network = 'public'
    AND (cid = $1 OR car_root = $1) AND state IN ('pending', 'pinned', 'unpinning'))::boolean;

-- name: FinishIPFSCopyCleanup :execrows
WITH locked AS MATERIALIZED (
    SELECT singleton FROM ipfs_capacity cap WHERE cap.singleton
      AND cap.maintenance_token = sqlc.arg(maintenance_token) FOR UPDATE
), removed AS (
    DELETE FROM ipfs_copy_cleanup
    WHERE claim_token = sqlc.arg(claim_token) AND cid = sqlc.arg(cid)
      AND EXISTS (SELECT 1 FROM locked)
    RETURNING cid
)
UPDATE ipfs_capacity b
SET cleanup_pending = b.cleanup_pending - (SELECT count(*) FROM removed),
    maintenance_token = NULL, maintenance_until = NULL, measure_after = now()
WHERE b.singleton AND b.maintenance_token = sqlc.arg(maintenance_token)
  AND EXISTS (SELECT 1 FROM removed);

-- name: RecoverIPFSCopyCleanup :execrows
-- Only called after a later confirmed node restart. Lease expiry by itself
-- cannot prove that an interrupted unpin/GC request has stopped on the server.
UPDATE ipfs_capacity SET maintenance_token = NULL, maintenance_until = NULL, measure_after = now()
WHERE singleton AND maintenance_token = sqlc.arg(maintenance_token) AND maintenance_until <= now();
