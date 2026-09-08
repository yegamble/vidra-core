-- Inbound federation persistence (migration 0037, .ralph/specs/federation.md §6,8).

-- name: IsActivityProcessed :one
SELECT EXISTS (SELECT 1 FROM federation_inbox_activities WHERE activity_id = $1);

-- name: MarkActivityProcessed :exec
INSERT INTO federation_inbox_activities (activity_id)
VALUES ($1)
ON CONFLICT (activity_id) DO NOTHING;

-- name: InsertRemoteFollow :exec
-- A remote actor follows a local channel. Channels are open → auto-accepted.
INSERT INTO remote_follows (channel_id, remote_actor_url, state, follow_activity_url)
VALUES ($1, $2, 'accepted', $3)
ON CONFLICT (channel_id, remote_actor_url)
DO UPDATE SET state = 'accepted', follow_activity_url = EXCLUDED.follow_activity_url;

-- name: CountRemoteFollowers :one
SELECT count(*) FROM remote_follows WHERE channel_id = $1 AND state = 'accepted';

-- name: DeleteRemoteFollow :exec
-- A remote actor un-following a local channel (inbound Undo{Follow}). Idempotent.
DELETE FROM remote_follows WHERE channel_id = $1 AND remote_actor_url = $2;

-- name: ListRemoteFollowerInboxes :many
-- Distinct inbox URLs (shared inbox preferred) of a channel's accepted remote
-- followers — the fan-out targets when the channel publishes a video.
SELECT DISTINCT COALESCE(ra.shared_inbox_url, ra.inbox_url) AS inbox
FROM remote_follows rf
JOIN remote_actors ra ON ra.actor_url = rf.remote_actor_url
WHERE rf.channel_id = $1
  AND rf.state = 'accepted'
  AND COALESCE(ra.shared_inbox_url, ra.inbox_url) <> '';

-- name: InsertFederatedVideoTombstone :exec
-- Record that a federated (public) video's ActivityPub id once existed, so a
-- peer dereferencing the Delete we just sent gets 410 + Tombstone rather than
-- the frontend's soft-404 page (A29-F9). Idempotent: a re-delete keeps the
-- FIRST deletion time, which is the one the Tombstone should carry.
INSERT INTO federated_video_tombstones (video_id)
VALUES ($1)
ON CONFLICT (video_id) DO NOTHING;

-- name: GetFederatedVideoTombstone :one
SELECT video_id, deleted_at FROM federated_video_tombstones WHERE video_id = $1;

-- name: BlockRemoteActor :exec
-- A viewer blocks one remote PERSON rather than their whole instance (A29-F7).
-- Addressed by actor URL, not by a remote_actors FK: the block must be possible
-- against an actor this instance has never cached, and an actor-cache eviction
-- must never silently lift it.
INSERT INTO remote_actor_blocks (blocker_id, remote_actor_url)
VALUES ($1, $2)
ON CONFLICT (blocker_id, remote_actor_url) DO NOTHING;

-- name: UnblockRemoteActor :execrows
DELETE FROM remote_actor_blocks WHERE blocker_id = $1 AND remote_actor_url = $2;

-- name: IsRemoteActorBlockedBy :one
-- Through remote_actor_block_reach (0142), so a block taken against an ACCOUNT
-- also refuses activity from every channel actor that account owns. Restricted
-- to the viewer's OWN rows: the instance-wide half has its own query, because a
-- caller that conflates them cannot tell an admin decision from a viewer's.
SELECT EXISTS (
    SELECT 1 FROM remote_actor_block_reach
    WHERE blocker_id = $1 AND actor_url = $2
);

-- name: IsRemoteActorBlockedByAnyone :one
-- The INBOUND gate: does ANY local viewer block this actor (or the account that
-- owns it)? A Note or Follow from an actor nobody blocks takes the ordinary
-- path; one from an actor some viewer blocks needs the per-viewer decision,
-- which the caller then makes.
SELECT EXISTS (
    SELECT 1 FROM remote_actor_block_reach
    WHERE blocker_id IS NOT NULL AND actor_url = $1
);

-- name: ListRemoteActorBlocks :many
-- A viewer's remote blocks, newest first, joined to the cached actor row when
-- there is one so the settings surface can render a handle rather than a URL.
-- The LEFT JOIN is deliberate: a block against an uncached actor still lists.
SELECT b.remote_actor_url, b.created_at,
       COALESCE(ra.preferred_username, '')::text AS preferred_username,
       COALESCE(ra.domain, '')::text AS domain
FROM remote_actor_blocks b
LEFT JOIN remote_actors ra ON ra.actor_url = b.remote_actor_url
WHERE b.blocker_id = $1
ORDER BY b.created_at DESC, b.remote_actor_url
LIMIT $2 OFFSET $3;

-- name: CountRemoteActorBlocks :one
SELECT count(*)::bigint FROM remote_actor_blocks WHERE blocker_id = $1;
