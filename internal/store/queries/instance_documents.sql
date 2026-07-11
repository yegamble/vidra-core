-- name: ListInstanceDocuments :many
-- Every stored instance document, ordered by name. The instancedocs service
-- loads these at boot (and after each write) into its in-memory cache so the
-- hot public delivery paths (GET /instance, /instance/homepage,
-- /instance/custom.css, /instance/custom.js) never round-trip to the database.
SELECT name, body, sha256, updated_by, updated_at
FROM instance_documents
ORDER BY name;

-- name: UpsertInstanceDocument :one
-- Set one instance document (insert or update), stamping the writing admin,
-- the content hash, and the update time.
INSERT INTO instance_documents (name, body, sha256, updated_by, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (name) DO UPDATE
SET body = EXCLUDED.body, sha256 = EXCLUDED.sha256,
    updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING name, body, sha256, updated_by, updated_at;

-- name: DeleteInstanceDocument :execrows
-- Remove one instance document (a PUT with an empty body clears it; the public
-- delivery routes 404 while unset). Returns the number of rows removed.
DELETE FROM instance_documents WHERE name = $1;
