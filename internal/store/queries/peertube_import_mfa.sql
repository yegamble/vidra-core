-- name: ImportHoldUserForMFA :execrows
-- Caller obtained this id from the importer-owned resync set. Keep ownership in
-- SQL as well; skip/merge mappings and native accounts are not ours to disable.
-- Pending enrollment is not protection. Never reactivate an existing hold.
UPDATE users u
SET is_active = false, updated_at = now()
WHERE u.id = $1 AND u.is_active AND u.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM peertube_import_ledger l
              WHERE l.entity_kind = 'user' AND l.vidra_id = u.id
                AND l.status = 'done' AND l.created_by_import)
  AND NOT EXISTS (SELECT 1 FROM user_mfa m WHERE m.user_id = u.id AND m.enabled);
