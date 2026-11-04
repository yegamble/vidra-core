-- name: RetireCatalogPending :execrows
-- Caller observed permanent receiver expiry, under brief source authority.
-- Preserve allocator/history/current verified proof and require a newer FULL
-- before deleting this exact pending payload. A crash then leaves durable work.
WITH matched AS MATERIALIZED (
    SELECT h.scope,h.epoch,h.generation
    FROM search_catalog_state s JOIN search_catalog_snapshots h USING(scope,epoch)
    WHERE s.scope='local' AND s.epoch=sqlc.arg(epoch)::uuid
      AND h.pending AND h.generation=sqlc.arg(generation)::bigint
      AND s.allocated_generation=h.generation
      AND (s.verified_generation IS NULL OR s.verified_generation<h.generation)
      AND h.page_count=sqlc.arg(page_count)::integer
      AND h.document_count=sqlc.arg(document_count)::bigint
      AND h.membership_sha256=sqlc.arg(membership_sha256)::text
      AND h.pages_sha256=sqlc.arg(pages_sha256)::text
      AND s.force_full=sqlc.arg(expected_force_full)::boolean
      AND s.force_full_after_generation=sqlc.arg(expected_force_full_after_generation)::bigint
      AND s.last_error_id IS NOT DISTINCT FROM sqlc.narg(expected_error_id)::uuid
    FOR UPDATE OF s,h
), fenced AS (
    UPDATE search_catalog_state s
    SET force_full=true,force_full_after_generation=GREATEST(s.force_full_after_generation,s.allocated_generation)
    FROM matched m WHERE s.scope=m.scope AND s.epoch=m.epoch
    RETURNING m.scope,m.epoch,m.generation
)
DELETE FROM search_catalog_snapshots h USING fenced f
WHERE h.scope=f.scope AND h.epoch=f.epoch AND h.generation=f.generation AND h.pending;
