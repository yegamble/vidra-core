-- Federation policy gates (migration 0087, config-parity W12): the pending
-- channel-follower approval queue (federation_follower_approval) and the
-- instance-initiated follow-backs of accepted followers
-- (federation_auto_follow_back). All of this governs the ActivityPub inbox
-- only — ATProto has no inbound path in vidra.

-- name: InsertRemoteFollowPending :exec
-- A remote actor follows a local channel while federation_follower_approval is
-- on: the follow waits in the admin queue (state 'pending'; the Accept is only
-- delivered on approval). An existing row — accepted OR pending — is left
-- untouched: the gate is never retroactive and a re-Follow never downgrades an
-- accepted follower.
INSERT INTO remote_follows (channel_id, remote_actor_url, state, follow_activity_url)
VALUES ($1, $2, 'pending', $3)
ON CONFLICT (channel_id, remote_actor_url) DO NOTHING;

-- name: ListPendingRemoteFollows :many
-- The admin follower-approval queue, newest first, with the followed channel's
-- handle and the follower's cached identity (empty strings when the actor
-- cache row is gone).
SELECT rf.id, rf.channel_id, rf.remote_actor_url, rf.follow_activity_url, rf.created_at,
       c.handle AS channel_handle,
       COALESCE(ra.preferred_username, '')::text AS preferred_username,
       COALESCE(ra.domain, '')::text AS domain
FROM remote_follows rf
JOIN channels c ON c.id = rf.channel_id
LEFT JOIN remote_actors ra ON ra.actor_url = rf.remote_actor_url
WHERE rf.state = 'pending'
ORDER BY rf.created_at DESC, rf.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountPendingRemoteFollows :one
-- How many rows ListPendingRemoteFollows would return, ignoring pagination. The
-- channels JOIN is part of the predicate (a follow whose channel is gone is not
-- listed) and the state filter must stay identical.
SELECT count(*)::bigint
FROM remote_follows rf
JOIN channels c ON c.id = rf.channel_id
WHERE rf.state = 'pending';

-- name: AcceptPendingRemoteFollowByID :one
-- Admin approval: flips one pending follow to accepted and returns what the
-- Accept delivery needs. Resolved/unknown ids match no row (pgx.ErrNoRows).
UPDATE remote_follows SET state = 'accepted'
WHERE id = $1 AND state = 'pending'
RETURNING channel_id, remote_actor_url, follow_activity_url;

-- name: DeletePendingRemoteFollowByID :one
-- Admin rejection: removes one pending follow and returns what the Reject
-- delivery needs. Resolved/unknown ids match no row (pgx.ErrNoRows).
DELETE FROM remote_follows
WHERE id = $1 AND state = 'pending'
RETURNING channel_id, remote_actor_url, follow_activity_url;

-- name: InsertChannelFollowBackIfAbsent :execrows
-- Records the auto-follow-back intent (loop guard): at most ONE follow-back
-- per remote actor instance-wide — the WHERE NOT EXISTS covers other channels'
-- rows, the ON CONFLICT covers this channel's. 0 rows = a follow-back already
-- exists, the caller must not enqueue another Follow.
INSERT INTO channel_follow_backs (channel_id, remote_actor_url, follow_activity_url)
SELECT $1, $2, $3
WHERE NOT EXISTS (SELECT 1 FROM channel_follow_backs WHERE remote_actor_url = $2)
ON CONFLICT (channel_id, remote_actor_url) DO NOTHING;

-- name: AcceptChannelFollowBackByActivity :execrows
-- Inbound Accept{Follow} of one of our follow-backs: flips it to accepted. The
-- remote_actor_url predicate enforces that the Accept's signer IS the followed
-- actor (mirrors AcceptRemoteChannelFollowByActivity).
UPDATE channel_follow_backs SET state = 'accepted'
WHERE follow_activity_url = $1 AND remote_actor_url = $2;

-- name: DeleteChannelFollowBackByActivity :execrows
-- Inbound Reject{Follow} of one of our follow-backs: removes it (same
-- signer-is-the-followed-actor predicate).
DELETE FROM channel_follow_backs
WHERE follow_activity_url = $1 AND remote_actor_url = $2;
