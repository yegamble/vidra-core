-- Inbound ActivityPub Move (migration 0167).

-- name: ClaimRemoteActorMove :execrows
-- Claims the right to act on a Move from origin_actor_url: 1 row = act, 0 = the
-- origin already moved within the cooldown, so this Move is accepted and
-- ignored. Taken BEFORE the target is fetched, so a hostile origin cannot make
-- this instance fetch more than one target per window by renaming it, and
-- atomic, so two replicas receiving the same Move cannot both act on it.
INSERT INTO remote_actor_moves (origin_actor_url, target_actor_url)
VALUES ($1, $2)
ON CONFLICT (origin_actor_url) DO UPDATE
SET target_actor_url = EXCLUDED.target_actor_url, claimed_at = now(),
    state = 'claimed', touched_at = now()
WHERE remote_actor_moves.claimed_at < now() - interval '24 hours';

-- name: ReleaseRemoteActorMove :exec
-- The target did not verify (unreachable, or no alsoKnownAs back-reference):
-- give the claim back so a redelivered or corrected Move is not stranded for a
-- day. Only an unverified claim is released; a move under way is not undone.
DELETE FROM remote_actor_moves WHERE origin_actor_url = $1 AND state = 'claimed';

-- name: SetRemoteActorMoveState :exec
UPDATE remote_actor_moves SET state = $2, touched_at = now()
WHERE origin_actor_url = $1;

-- name: NextMovingRemoteActorMove :one
-- The move whose turn it is: the least recently touched 'moving' row, touched
-- now so the next call takes another, and SKIP LOCKED so concurrent workers
-- take different moves. pgx.ErrNoRows when nothing is moving.
UPDATE remote_actor_moves SET touched_at = now()
WHERE origin_actor_url = (
    SELECT origin_actor_url FROM remote_actor_moves
    WHERE state = 'moving'
    ORDER BY touched_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING origin_actor_url, target_actor_url;

-- name: CarryRemoteActorBlocks :execrows
-- A local user who blocked the actor that moved blocks where it moved to, as
-- Mastodon carries blocks over on a Move.
INSERT INTO remote_actor_blocks (blocker_id, remote_actor_url)
SELECT b.blocker_id, sqlc.arg('target_actor_url') FROM remote_actor_blocks b
WHERE b.remote_actor_url = sqlc.arg('origin_actor_url')
ON CONFLICT (blocker_id, remote_actor_url) DO NOTHING;

-- name: ListRemoteChannelFollowsOfActor :many
-- Local users' live follow edges (pending or accepted, never rejected) to a
-- remote actor that moved, oldest first, at most result_limit of them: a move is
-- re-pointed in bounded batches. A user who blocks the TARGET is left out — they
-- keep the old follow — so the list empties once the move is done.
SELECT f.id, f.user_id
FROM remote_channel_follows f
WHERE f.remote_actor_url = sqlc.arg('remote_actor_url') AND f.state IN ('pending', 'accepted')
  AND NOT EXISTS (
      SELECT 1 FROM remote_actor_block_reach r
      WHERE r.blocker_id = f.user_id AND r.actor_url = sqlc.arg('target_actor_url')
  )
ORDER BY f.created_at, f.id
LIMIT sqlc.arg('result_limit');
