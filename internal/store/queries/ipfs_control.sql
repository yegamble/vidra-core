-- name: EnsureIPFSControlConfig :one
INSERT INTO ipfs_control_config (config) VALUES ($1)
ON CONFLICT (singleton) DO UPDATE SET singleton = true
RETURNING *;

-- name: GetIPFSControlConfig :one
SELECT * FROM ipfs_control_config WHERE singleton;

-- name: UpdateIPFSControlConfig :one
-- UPDATE itself provides the CAS predicate recheck after a concurrent writer.
-- The complete config and its apply operation commit together.
WITH changed AS (
    UPDATE ipfs_control_config AS saved
    SET config = sqlc.arg(config), revision = saved.revision + 1, policy_active = true,
        updated_by = sqlc.narg(actor), updated_at = now()
    WHERE saved.singleton AND saved.revision = sqlc.arg(expected_revision)
      AND (saved.config <> sqlc.arg(config) OR NOT saved.policy_active)
    RETURNING saved.*
), queued AS (
    INSERT INTO ipfs_control_operations (id, config_revision, action, config, requested_by)
    SELECT sqlc.arg(operation_id), changed.revision, 'apply', changed.config, sqlc.narg(actor)
    FROM changed WHERE changed.config->>'provider' = 'internal'
    RETURNING id
), current AS (
    SELECT * FROM changed
    UNION ALL
    SELECT * FROM ipfs_control_config
    WHERE singleton AND revision = sqlc.arg(expected_revision)
      AND config = sqlc.arg(config) AND policy_active AND NOT EXISTS (SELECT 1 FROM changed)
)
SELECT current.*, COALESCE(queued.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS operation_id
FROM current LEFT JOIN queued ON true;

-- name: RequestIPFSControlOperation :one
-- Replaying an accepted UUID returns its original result even if the config has
-- since advanced. Reusing an ID for a different request never queues anything.
WITH prior AS (
    SELECT op.* FROM ipfs_control_operations AS op WHERE op.id = sqlc.arg(id)
), created AS (
    INSERT INTO ipfs_control_operations (id, config_revision, action, config, requested_by)
    SELECT sqlc.arg(id), saved.revision, sqlc.arg(action), saved.config, sqlc.narg(actor)
    FROM ipfs_control_config AS saved
    WHERE saved.singleton AND saved.revision = sqlc.arg(expected_revision)
      AND saved.config->>'provider' = 'internal' AND NOT EXISTS (SELECT 1 FROM prior)
    ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id
    WHERE ipfs_control_operations.action = EXCLUDED.action
      AND ipfs_control_operations.config_revision = EXCLUDED.config_revision
      AND ipfs_control_operations.config = EXCLUDED.config
    RETURNING *
)
SELECT * FROM created
UNION ALL
SELECT * FROM prior WHERE config_revision = sqlc.arg(expected_revision) AND action = sqlc.arg(action);

-- name: GetIPFSControlOperation :one
SELECT * FROM ipfs_control_operations WHERE id = $1;

-- name: NextIPFSControlOperation :one
-- Never skip an older running operation and let a newer sequence race ahead.
SELECT * FROM ipfs_control_operations WHERE state IN ('pending', 'running')
ORDER BY sequence LIMIT 1;

-- name: ObserveIPFSControlOperation :execrows
UPDATE ipfs_control_operations
SET state = sqlc.arg(state), last_error_code = sqlc.narg(error_code),
    attempts = attempts + 1, next_attempt_at = now() + interval '5 seconds', updated_at = now()
WHERE id = sqlc.arg(id) AND sequence = sqlc.arg(sequence)
  AND state IN ('pending', 'running');

-- name: LatestIPFSControlOperation :one
SELECT * FROM ipfs_control_operations ORDER BY sequence DESC LIMIT 1;
