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
