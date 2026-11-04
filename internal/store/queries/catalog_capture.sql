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
