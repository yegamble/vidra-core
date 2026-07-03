-- Account lifecycle queries (fix_plan P4 + product-decisions.md §1).
--
-- The §1 hard delete anonymises the users row rather than removing it, so the
-- ON DELETE CASCADE FKs hanging off users never fire — every user-owned row
-- that must go is deleted EXPLICITLY here. Content rows that must survive
-- (comment tombstones, DM messages, audit rows, reports) are intentionally
-- absent from this file.

-- name: DeleteVideoRatingsByUser :exec
DELETE FROM video_ratings WHERE user_id = $1;

-- name: DeleteSavedVideosByUser :exec
DELETE FROM saved_videos WHERE user_id = $1;

-- name: DeletePlaylistsByOwner :exec
-- playlist_items cascade with their playlist.
DELETE FROM playlists WHERE owner_id = $1;

-- name: DeleteChannelFollowsByFollower :exec
DELETE FROM channel_follows WHERE follower_id = $1;

-- name: DeleteRemoteChannelFollowsByUser :exec
DELETE FROM remote_channel_follows WHERE user_id = $1;

-- name: DeleteMutedAccountsByUser :exec
-- Both directions: the account's own mutes AND stale mutes of the now-deleted
-- account held by others.
DELETE FROM muted_accounts WHERE muter_id = $1 OR muted_id = $1;

-- name: DeleteMutedInstancesByUser :exec
DELETE FROM muted_instances WHERE muter_id = $1;

-- name: DeleteUserBlocksByUser :exec
-- Both directions, like account mutes.
DELETE FROM user_blocks WHERE blocker_id = $1 OR blocked_id = $1;

-- name: DeleteOAuthIdentitiesByUser :exec
DELETE FROM oauth_identities WHERE user_id = $1;

-- name: DeleteNotificationsByUser :exec
DELETE FROM notifications WHERE user_id = $1;

-- name: DeleteNotificationPrefsByUser :exec
DELETE FROM notification_prefs WHERE user_id = $1;

-- name: DeletePasswordResetTokensByUser :exec
DELETE FROM password_reset_tokens WHERE user_id = $1;

-- name: DeleteEmailVerificationTokensByUser :exec
DELETE FROM email_verification_tokens WHERE user_id = $1;

-- name: DeleteAccountActorKey :exec
-- The account's ActivityPub actor keypair (channel actor keys cascade with
-- their channel rows, which the delete flow removes explicitly).
DELETE FROM account_actor_keys WHERE user_id = $1;

-- Export data reads (P4 account export). Ordered oldest-first so archives are
-- stable/diffable across exports of unchanged data.

-- name: ListVideosByOwnerForExport :many
-- Every video the account owns (all states/privacies — it is the owner's own
-- data), with its channel handle for re-association.
SELECT v.id, c.handle AS channel_handle, v.title, v.description, v.privacy,
       v.state, v.category, v.language, v.license, v.publish_at,
       v.created_at, v.updated_at
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE c.owner_id = $1
ORDER BY v.created_at ASC, v.id ASC;

-- name: ListCommentsByUserForExport :many
-- The account's own (non-tombstoned) comments.
SELECT id, video_id, body, created_at, updated_at
FROM comments
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at ASC, id ASC;

-- name: ListFollowedChannelsByUser :many
SELECT cf.channel_id, ch.handle, ch.display_name, cf.created_at
FROM channel_follows cf
JOIN channels ch ON ch.id = cf.channel_id
WHERE cf.follower_id = $1
ORDER BY cf.created_at ASC, cf.channel_id ASC;

-- name: ListSavedVideosByUserForExport :many
SELECT video_id, created_at
FROM saved_videos
WHERE user_id = $1
ORDER BY created_at ASC, video_id ASC;

-- name: ListWatchHistoryByUserForExport :many
SELECT video_id, position_seconds, updated_at
FROM watch_history
WHERE user_id = $1
ORDER BY updated_at ASC, video_id ASC;
