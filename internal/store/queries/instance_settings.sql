-- name: ListInstanceSettings :many
-- Every stored instance-setting override, ordered by key. The settings service
-- loads these at boot and after each write to build its in-memory cache; keys
-- with no row fall back to the config default.
SELECT key, value, updated_by, updated_at
FROM instance_settings
ORDER BY key;

-- name: UpsertInstanceSetting :exec
-- Set one instance setting override (insert or update), stamping the writing
-- admin and the update time.
INSERT INTO instance_settings (key, value, updated_by, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: DeleteInstanceSetting :execrows
-- Remove one instance setting override, resetting the key to its config
-- default. Returns the number of rows removed (0 when the key was not overridden).
DELETE FROM instance_settings WHERE key = $1;

-- name: GetInstanceSetting :one
-- One stored override by key, or no rows when the key is not overridden. The
-- settings service reads the whole set into its cache; this is for a writer
-- OUTSIDE the request path (the PeerTube import) that has to see the row as it
-- stands in the database rather than as some process cached it.
SELECT key, value, updated_by, updated_at
FROM instance_settings
WHERE key = $1;
