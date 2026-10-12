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
SET target_actor_url = EXCLUDED.target_actor_url, claimed_at = now()
WHERE remote_actor_moves.claimed_at < now() - interval '24 hours';

-- name: ListRemoteChannelFollowsOfActor :many
-- Local users' live follow edges (pending or accepted, never rejected) to a
-- remote actor that moved, oldest first, at most result_limit of them: one Move
-- migrates a bounded batch, and edges left behind are still on the old actor,
-- where a later Move reaches them.
SELECT id, user_id
FROM remote_channel_follows
WHERE remote_actor_url = $1 AND state IN ('pending', 'accepted')
ORDER BY created_at, id
LIMIT sqlc.arg('result_limit');
