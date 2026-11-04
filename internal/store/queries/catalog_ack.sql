-- name: AcknowledgeCatalogFull :execrows
-- Caller independently verified the receiver's exact completed FULL manifest.
-- Run inside brief source authority, never across HTTP. An older response may
-- not erase a newer force request or repeated same-code error evidence.
WITH matched AS MATERIALIZED (
    SELECT h.scope,h.epoch,h.generation
    FROM search_catalog_state s
    JOIN search_catalog_snapshots h USING(scope,epoch)
    WHERE s.scope='local' AND s.epoch=sqlc.arg(epoch)::uuid
      AND h.pending AND h.generation=sqlc.arg(generation)::bigint
      AND h.page_count=sqlc.arg(page_count)::integer
      AND h.document_count=sqlc.arg(document_count)::bigint
      AND h.membership_sha256=sqlc.arg(membership_sha256)::text
      AND h.pages_sha256=sqlc.arg(pages_sha256)::text
      AND s.force_full=sqlc.arg(expected_force_full)::boolean
      AND s.force_full_after_generation=sqlc.arg(expected_force_full_after_generation)::bigint
      AND s.last_error_id IS NOT DISTINCT FROM sqlc.narg(expected_error_id)::uuid
      AND (s.verified_generation IS NULL OR s.verified_generation<h.generation)
    FOR UPDATE OF s,h
), completed AS (
    UPDATE search_catalog_snapshots h SET pending=false
    FROM matched m
    WHERE h.scope=m.scope AND h.epoch=m.epoch AND h.generation=m.generation
    RETURNING h.scope,h.epoch,h.generation
)
UPDATE search_catalog_state s
SET verified_generation=c.generation,verified_at=now(),
    force_full=s.force_full AND c.generation<=s.force_full_after_generation,
    last_error_code=NULL,last_error_id=NULL
FROM completed c
WHERE s.scope=c.scope AND s.epoch=c.epoch;
