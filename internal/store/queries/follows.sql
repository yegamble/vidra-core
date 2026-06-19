-- name: FollowChannel :exec
INSERT INTO channel_follows (follower_id, channel_id)
VALUES ($1, $2)
ON CONFLICT (follower_id, channel_id) DO NOTHING;

-- name: UnfollowChannel :exec
DELETE FROM channel_follows
WHERE follower_id = $1 AND channel_id = $2;

-- name: CountChannelFollowers :one
SELECT count(*) FROM channel_follows WHERE channel_id = $1;

-- name: IsFollowingChannel :one
SELECT EXISTS (
    SELECT 1 FROM channel_follows
    WHERE follower_id = $1 AND channel_id = $2
);
