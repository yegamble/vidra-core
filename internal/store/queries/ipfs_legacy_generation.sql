-- name: RecordLegacyIPFSGeneration :execrows
-- This receipt follows a successful legacy AddDirectory, never a metadata
-- backfill. Lock policy and the ready master so adoption/promotion serialize
-- with certification; a missing policy row cannot certify a copy.
WITH pinned AS MATERIALIZED (
    SELECT p.object_key, p.video_id FROM media_ipfs_pins p
    WHERE p.object_key = sqlc.arg(object_key) AND p.state = 'pinned'
      AND p.network = 'public' AND p.media_class = 'hls'
      AND p.policy_reason = 'legacy' AND p.claim_token IS NULL
      AND p.cid = sqlc.arg(cid) AND p.car_root = sqlc.arg(cid) AND p.cid <> ''
      AND p.object_key = 'streaming-playlists/' || p.video_id::text || '/'
    FOR UPDATE
), policy AS MATERIALIZED (
    SELECT singleton FROM ipfs_control_config
    WHERE singleton AND NOT policy_active AND EXISTS (SELECT 1 FROM pinned)
    FOR SHARE
), generation AS MATERIALIZED (
    SELECT sp.video_id FROM streaming_playlists sp
    WHERE sp.video_id IN (SELECT video_id FROM pinned) AND sp.state = 'ready'
      AND sp.master_key = sqlc.arg(generation) AND sp.master_key <> ''
      AND EXISTS (SELECT 1 FROM policy)
    FOR SHARE
)
UPDATE media_ipfs_pins SET committed_generation = sqlc.arg(generation)
WHERE object_key IN (SELECT object_key FROM pinned)
  AND video_id IN (SELECT video_id FROM generation);
