-- Federation actor keypairs (see migration 0035, .ralph/specs/federation.md §2-3).
-- Keys are minted lazily on first federation use: the service reads the row, and
-- if absent (pgx.ErrNoRows) mints a keypair and INSERTs it. The insert is
-- ON CONFLICT DO NOTHING so two concurrent minters don't clobber each other — the
-- loser re-reads the winner's row.

-- name: GetAccountActorKey :one
SELECT public_key_pem, private_key_pem FROM account_actor_keys WHERE user_id = $1;

-- name: InsertAccountActorKeyIfAbsent :execrows
INSERT INTO account_actor_keys (user_id, public_key_pem, private_key_pem)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO NOTHING;

-- name: GetChannelActorKey :one
SELECT public_key_pem, private_key_pem FROM channel_actor_keys WHERE channel_id = $1;

-- name: InsertChannelActorKeyIfAbsent :execrows
INSERT INTO channel_actor_keys (channel_id, public_key_pem, private_key_pem)
VALUES ($1, $2, $3)
ON CONFLICT (channel_id) DO NOTHING;
