-- Channel collaborators (migration 0097): editors a channel owner invites to
-- manage the channel's content.

-- name: AddChannelMember :one
INSERT INTO channel_members (channel_id, user_id, role, invited_by)
VALUES ($1, $2, $3, $4)
RETURNING channel_id, user_id, role, invited_by, created_at;

-- name: GetChannelMember :one
SELECT channel_id, user_id, role, invited_by, created_at
FROM channel_members
WHERE channel_id = $1 AND user_id = $2;

-- name: DeleteChannelMember :execrows
DELETE FROM channel_members WHERE channel_id = $1 AND user_id = $2;

-- name: ListChannelMembers :many
-- Members of a channel, oldest first, joined to the user for the display fields.
SELECT cm.user_id, cm.role, cm.invited_by, cm.created_at,
       u.username, u.display_name
FROM channel_members cm
JOIN users u ON u.id = cm.user_id
WHERE cm.channel_id = $1
ORDER BY cm.created_at;

-- name: IsChannelManager :one
-- Whether a user may manage a channel's content: the owner OR an editor member.
-- The single authorization primitive behind canManageChannelContent.
SELECT EXISTS (
    SELECT 1 FROM channels c
    WHERE c.id = sqlc.arg('channel_id')
      AND (c.owner_id = sqlc.arg('user_id')
           OR EXISTS (SELECT 1 FROM channel_members m
                       WHERE m.channel_id = c.id AND m.user_id = sqlc.arg('user_id')))
);

-- name: ListChannelsForMember :many
-- Channels the user is an editor member of (NOT owner), with their role — the
-- "shared with you" half of GET /me/channels.
SELECT c.id, c.owner_id, c.handle, c.display_name, c.description,
       c.created_at, c.updated_at, c.activitypub_enabled, c.atproto_enabled,
       cm.role
FROM channel_members cm
JOIN channels c ON c.id = cm.channel_id
WHERE cm.user_id = $1
ORDER BY c.created_at;
