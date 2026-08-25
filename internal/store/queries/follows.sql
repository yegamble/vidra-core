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

-- name: CountFollowersByOwner :many
-- Follower count for every channel a user owns, in one grouped query — the
-- channel-domain half of the account stats rollup (GET /me/stats). Channels
-- with no followers appear with 0 via the LEFT JOIN.
SELECT c.id AS channel_id, count(cf.follower_id)::bigint AS followers
FROM channels c
LEFT JOIN channel_follows cf ON cf.channel_id = c.id
WHERE c.owner_id = $1
GROUP BY c.id;

-- name: IsFollowingChannel :one
SELECT EXISTS (
    SELECT 1 FROM channel_follows
    WHERE follower_id = $1 AND channel_id = $2
);

-- name: GetFollowNotificationSetting :one
-- The caller's bell mode for one channel (migration 0101). NO ROW means the
-- caller does not follow the channel at all — callers distinguish that from a
-- muted bell ('none').
SELECT notification_setting FROM channel_follows
WHERE follower_id = $1 AND channel_id = $2;

-- name: SetFollowNotificationSetting :execrows
-- Set the bell mode on an EXISTING follow. Returns the number of rows updated so
-- the caller can refuse (404) a bell set on a channel the caller does not follow
-- — the bell is a property of a subscription, never a standalone preference.
UPDATE channel_follows
SET notification_setting = sqlc.arg('notification_setting')
WHERE follower_id = sqlc.arg('follower_id') AND channel_id = sqlc.arg('channel_id');

-- name: ListFollowedChannels :many
-- The LOCAL channels the caller follows (the "FOLLOWING" list), most recently
-- followed first. follower_count is each channel's total follower count,
-- followed_at is when the caller followed it, and notification_setting is the
-- caller's bell for that channel (migration 0101) so the list can render its
-- state without an extra request per row. Paginated via limit/offset.
SELECT
    c.id, c.owner_id, c.handle, c.display_name, c.description,
    c.created_at, c.updated_at, c.activitypub_enabled, c.atproto_enabled,
    (SELECT count(*) FROM channel_follows cf2 WHERE cf2.channel_id = c.id) AS follower_count,
    cf.created_at AS followed_at, cf.notification_setting
FROM channel_follows cf
JOIN channels c ON c.id = cf.channel_id
WHERE cf.follower_id = $1
ORDER BY cf.created_at DESC, c.id
LIMIT $2 OFFSET $3;

-- name: CountFollowedChannels :one
-- How many rows ListFollowedChannels would return, ignoring pagination. The
-- channels JOIN is part of the predicate.
SELECT count(*)::bigint
FROM channel_follows cf
JOIN channels c ON c.id = cf.channel_id
WHERE cf.follower_id = $1;
