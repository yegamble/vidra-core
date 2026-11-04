-- name: RecordCatalogDeliveryError :execrows
-- Called under brief source authority after HTTP has finished. Rotate even an
-- identical sanitized code: stale ACK/error work must compare current evidence.
UPDATE search_catalog_state s
SET last_error_code=sqlc.arg(error_code)::text,last_error_id=uuid_generate_v4()
FROM search_catalog_snapshots h
WHERE s.scope='local' AND h.scope=s.scope AND h.epoch=s.epoch
  AND s.epoch=sqlc.arg(epoch)::uuid
  AND h.pending AND h.generation=sqlc.arg(generation)::bigint
  AND h.page_count=sqlc.arg(page_count)::integer
  AND h.document_count=sqlc.arg(document_count)::bigint
  AND h.membership_sha256=sqlc.arg(membership_sha256)::text
  AND h.pages_sha256=sqlc.arg(pages_sha256)::text
  AND s.force_full=sqlc.arg(expected_force_full)::boolean
  AND s.force_full_after_generation=sqlc.arg(expected_force_full_after_generation)::bigint
  AND s.last_error_id IS NOT DISTINCT FROM sqlc.narg(expected_error_id)::uuid;
