-- name: ConvertLegacyCatalog :one
-- No sequence/time high-water: a lower ID can commit after this batch.
-- Retry leases do not remove an outstanding catalog obligation.
WITH selected AS MATERIALIZED (
    SELECT id FROM search_outbox
    WHERE state IN ('pending', 'dead')
      AND event_type IN ('video.upsert', 'video.suppress', 'channel.upsert',
                        'channel.delete', 'user.suppress', 'reconcile.begin',
                        'reconcile.page', 'reconcile.end')
      AND EXISTS (SELECT 1 FROM search_catalog_state WHERE scope = 'local')
    ORDER BY id
    LIMIT sqlc.arg('batch_size')::integer
    FOR UPDATE SKIP LOCKED
), retired AS (
    DELETE FROM search_outbox o USING selected s WHERE o.id = s.id
    RETURNING o.id
), hint AS (
    INSERT INTO search_catalog_dirty(scope)
    SELECT 'local' WHERE EXISTS (SELECT 1 FROM retired)
    RETURNING id
)
SELECT count(*)::bigint FROM retired WHERE EXISTS (SELECT 1 FROM hint);
