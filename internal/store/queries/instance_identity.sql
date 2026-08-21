-- The install's own identity (migration 0105). One row, written once by the
-- migration and never updated, so this is the whole interface: read it.

-- name: GetInstanceIdentity :one
-- The UUID media GC stamps into the object store's ownership marker
-- (storage.OwnerMarkerKey) to record which install the bucket belongs to. The
-- ORDER BY is defensive rather than meaningful — the table holds one row — but a
-- query that could return a different answer per call would be the wrong thing
-- to hang a delete decision on.
SELECT id FROM instance_identity ORDER BY created_at, id LIMIT 1;
