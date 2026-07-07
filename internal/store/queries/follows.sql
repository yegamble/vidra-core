-- name: FollowChannel :execrows
-- Idempotent follow. Returns the number of rows inserted (1 = a new follow, 0 =
-- already following) so callers can fire a notification only on a new follow.
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

-- name: ListFollowedChannels :many
-- The LOCAL channels the caller follows (the "FOLLOWING" list), most recently
-- followed first. follower_count is each channel's total follower count and
-- followed_at is when the caller followed it. Paginated via limit/offset.
SELECT
    c.id, c.owner_id, c.handle, c.display_name, c.description,
    c.created_at, c.updated_at,
    (SELECT count(*) FROM channel_follows cf2 WHERE cf2.channel_id = c.id) AS follower_count,
    cf.created_at AS followed_at
FROM channel_follows cf
JOIN channels c ON c.id = cf.channel_id
WHERE cf.follower_id = $1
ORDER BY cf.created_at DESC, c.id
LIMIT $2 OFFSET $3;
