-- name: RecordCatalogCaptureError :execrows
-- Capture has already rolled back and released its RR connection. A delayed
-- failure cannot overwrite a later allocation, force request or pending FULL.
-- Invoke only inside brief source authority; raw errors never enter this field.
UPDATE search_catalog_state s
SET last_error_code=sqlc.arg(error_code)::text,last_error_id=uuid_generate_v4()
WHERE s.scope='local' AND s.epoch=sqlc.arg(epoch)::uuid
  AND s.allocated_generation=sqlc.arg(expected_allocated_generation)::bigint
  AND s.force_full=sqlc.arg(expected_force_full)::boolean
  AND s.force_full_after_generation=sqlc.arg(expected_force_full_after_generation)::bigint
  AND s.last_error_id IS NOT DISTINCT FROM sqlc.narg(expected_error_id)::uuid
  AND NOT EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE scope='local' AND pending);
