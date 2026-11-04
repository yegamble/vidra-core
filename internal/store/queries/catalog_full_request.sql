-- name: RequestCatalogFull :execrows
-- Run under brief source authority with the accepted operator audit in the same
-- transaction. A generation already frozen when this request commits cannot
-- satisfy it; only independent verification of a newer FULL clears the fence.
UPDATE search_catalog_state
SET force_full=true,force_full_after_generation=allocated_generation
WHERE scope='local' AND epoch=sqlc.arg(epoch)::uuid
  AND allocated_generation=sqlc.arg(expected_allocated_generation)::bigint;
