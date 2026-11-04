-- name: LockCatalogCaptureState :one
-- Must run after source session authority is acquired, inside the capture RR.
SELECT epoch, allocated_generation, force_full, force_full_after_generation,
       last_error_id,
       EXISTS(SELECT 1 FROM search_catalog_dirty WHERE scope='local') AS dirty,
       EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE scope='local' AND pending) AS pending
FROM search_catalog_state WHERE scope='local' FOR UPDATE;

-- name: InsertCatalogCapturePage :exec
-- Preserve the entire frozen request, never JSONB re-encode its wire bytes.
INSERT INTO search_catalog_pages(scope,epoch,generation,page_index,event_id,
    request_body,document_count,canonical_bytes,content_sha256)
VALUES ('local',$1,$2,$3,$4,$5,$6,$7,$8);

-- name: AckCatalogVisibleDirty :execrows
-- The same RR snapshot as metadata fixes exact immutable UUID membership.
-- Later commits remain invisible; no sequence, time or UUID high-water applies.
WITH visible AS MATERIALIZED (
    SELECT id FROM search_catalog_dirty WHERE scope='local'
)
DELETE FROM search_catalog_dirty d USING visible v WHERE d.id=v.id AND d.scope='local';

-- name: AdvanceCatalogCapture :one
-- Dirty capture cannot make the old verified full ready when hints are ACKed.
-- All SET expressions use the prior allocator. Periodic no-dirty repair keeps
-- existing verified authority; an in-flight full is coalesced instead of replaced.
UPDATE search_catalog_state
SET allocated_generation=allocated_generation+1,
    force_full=force_full OR sqlc.arg(dirty)::boolean,
    force_full_after_generation=CASE WHEN sqlc.arg(dirty)::boolean
        THEN GREATEST(force_full_after_generation,allocated_generation)
        ELSE force_full_after_generation END
WHERE scope='local' AND epoch=sqlc.arg(epoch)::uuid
  AND allocated_generation=sqlc.arg(expected_allocated_generation)::bigint
  AND NOT EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE scope='local' AND pending)
RETURNING allocated_generation;

-- name: InsertCatalogCaptureSnapshot :exec
-- Header is written only after the full builder proves every staged page.
INSERT INTO search_catalog_snapshots(scope,epoch,generation,page_count,document_count,
    canonical_bytes,membership_sha256,pages_sha256,end_event_id,end_request_body)
VALUES ('local',$1,$2,$3,$4,$5,$6,$7,$8,$9);
