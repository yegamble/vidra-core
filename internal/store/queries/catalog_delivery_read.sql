-- name: ReadCatalogDeliveryState :one
-- One MVCC observation of source authority and retained proofs. QueryRow/Scan
-- must finish and release the pool connection before any receiver HTTP call.
SELECT s.epoch,s.allocated_generation,s.force_full,s.force_full_after_generation,
       s.last_error_id,s.last_error_code,e.receiver_predecessor,
       s.verified_generation,s.verified_at,
       v.page_count AS verified_page_count,v.document_count AS verified_document_count,
       v.membership_sha256 AS verified_membership_sha256,v.pages_sha256 AS verified_pages_sha256,
       p.generation AS pending_generation,p.page_count AS pending_page_count,
       p.document_count AS pending_document_count,p.canonical_bytes AS pending_canonical_bytes,
       p.membership_sha256 AS pending_membership_sha256,p.pages_sha256 AS pending_pages_sha256,
       p.end_request_body AS pending_end_request_body,p.captured_at AS pending_captured_at
FROM search_catalog_state s
JOIN search_catalog_epochs e USING(scope,epoch)
LEFT JOIN search_catalog_snapshots v
  ON v.scope=s.scope AND v.epoch=s.epoch AND v.generation=s.verified_generation
LEFT JOIN search_catalog_snapshots p ON p.scope=s.scope AND p.epoch=s.epoch AND p.pending
WHERE s.scope='local';

-- name: ReadCatalogDeliveryPage :one
-- Fetch one exact frozen body, not a live Rows cursor across network I/O.
-- Concurrent completion/bootstrap/retirement may remove this pending work.
SELECT p.request_body FROM search_catalog_pages p
JOIN search_catalog_snapshots h USING(scope,epoch,generation)
JOIN search_catalog_state s USING(scope,epoch)
WHERE p.scope='local' AND p.epoch=sqlc.arg(epoch)::uuid
  AND p.generation=sqlc.arg(generation)::bigint
  AND p.page_index=sqlc.arg(page_index)::integer AND h.pending;
