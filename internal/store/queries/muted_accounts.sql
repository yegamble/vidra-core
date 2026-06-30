-- name: MuteAccount :execrows
-- Mute an account (idempotent). The service rejects a self-mute before this runs;
-- an unknown target raises a foreign-key violation (SQLSTATE 23503) mapped to
-- "user not found".
INSERT INTO muted_accounts (muter_id, muted_id)
VALUES ($1, $2)
ON CONFLICT (muter_id, muted_id) DO NOTHING;

-- name: UnmuteAccount :execrows
-- Unmute an account (idempotent). Returns rows deleted (0 = it was not muted).
DELETE FROM muted_accounts WHERE muter_id = $1 AND muted_id = $2;

-- name: ListMutedAccounts :many
-- A user's muted accounts, newest mute first, with the muted account's identity.
SELECT m.muted_id, u.username, u.display_name, m.created_at
FROM muted_accounts m
JOIN users u ON u.id = m.muted_id
WHERE m.muter_id = $1
ORDER BY m.created_at DESC, m.muted_id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
