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
-- A user's muted accounts, newest mute first, with the muted account's identity
-- and the handles it publishes under.
--
-- channel_handles exists for ONE caller: the frontend's autosuggest filter
-- (A16 ruling). Autosuggest is viewer-agnostic by design — vidra-search's index
-- stores static eligibility and never per-viewer state, which is what makes the
-- ranked-ids contract visibility-safe — so the client drops a channel
-- suggestion naming a muted account itself. A suggestion carries only the
-- handle, so without this the client would need a lookup per suggested handle
-- per keystroke. The subquery is ordered so the array is stable, and an account
-- with no channel yields an empty array rather than NULL.
SELECT m.muted_id, u.username, u.display_name, m.created_at,
       ARRAY(
           SELECT c.handle FROM channels c WHERE c.owner_id = u.id ORDER BY c.handle
       )::text[] AS channel_handles
FROM muted_accounts m
JOIN users u ON u.id = m.muted_id
WHERE m.muter_id = $1
ORDER BY m.created_at DESC, m.muted_id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountMutedAccounts :one
-- How many rows ListMutedAccounts would return, ignoring pagination.
SELECT count(*)::bigint
FROM muted_accounts m
JOIN users u ON u.id = m.muted_id
WHERE m.muter_id = $1;
