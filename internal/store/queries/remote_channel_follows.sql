-- Outbound remote-channel follows: a LOCAL user following a REMOTE channel
-- (migration 0052, .ralph/specs/federation-remote-content.md §3).

-- name: UpsertRemoteChannelFollow :one
-- Records a user's follow of a remote channel. Idempotent: re-following keeps
-- the existing row (state + follow_activity_url untouched — the no-op DO UPDATE
-- only exists so the existing row is RETURNed). Callers detect a fresh insert
-- by comparing the returned follow_activity_url with the one they minted, and
-- only then enqueue the outbound Follow.
INSERT INTO remote_channel_follows (user_id, remote_actor_url, follow_activity_url)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, remote_actor_url) DO UPDATE SET user_id = remote_channel_follows.user_id
RETURNING id, user_id, remote_actor_url, state, follow_activity_url, created_at;

-- name: GetRemoteChannelFollowByID :one
-- One of the caller's follows, joined with the followed actor's identity and
-- inbox (the Undo{Follow} delivery target). Scoped by user_id so one user can
-- never address another's follow row.
SELECT f.id, f.user_id, f.remote_actor_url, f.state, f.follow_activity_url, f.created_at,
       ra.preferred_username, ra.domain,
       COALESCE(ra.shared_inbox_url, ra.inbox_url)::text AS inbox_url
FROM remote_channel_follows f
JOIN remote_actors ra ON ra.actor_url = f.remote_actor_url
WHERE f.id = $1 AND f.user_id = $2;

-- name: ListRemoteChannelFollows :many
-- The caller's remote follows, newest first, with the followed actor's identity.
SELECT f.id, f.remote_actor_url, f.state, f.created_at,
       ra.preferred_username, ra.domain
FROM remote_channel_follows f
JOIN remote_actors ra ON ra.actor_url = f.remote_actor_url
WHERE f.user_id = $1
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: DeleteRemoteChannelFollowByID :execrows
-- Local unfollow (local intent wins: the row goes immediately; the Undo is
-- queued separately). Scoped by user_id; rows report whether anything existed.
DELETE FROM remote_channel_follows WHERE id = $1 AND user_id = $2;

-- name: AcceptRemoteChannelFollowByActivity :execrows
-- Inbound Accept{Follow}: flips the matching pending follow to accepted. The
-- remote_actor_url predicate enforces that the Accept's signer IS the followed
-- actor — an Accept signed by anyone else matches no row.
UPDATE remote_channel_follows
SET state = 'accepted'
WHERE follow_activity_url = $1 AND remote_actor_url = $2;

-- name: DeleteRemoteChannelFollowByActivity :execrows
-- Inbound Reject{Follow}: removes the matching follow. Same signer-is-the-
-- followed-actor predicate as the Accept.
DELETE FROM remote_channel_follows
WHERE follow_activity_url = $1 AND remote_actor_url = $2;

-- name: HasRemoteChannelFollow :one
-- Any outbound follow edge (pending OR accepted) from a local user to this
-- remote actor. The auto-follow-back loop guard (config-parity W12): when a
-- local user already asked to follow the actor, the instance never adds a
-- redundant channel follow-back.
SELECT EXISTS (
    SELECT 1 FROM remote_channel_follows
    WHERE remote_actor_url = $1
);

-- name: HasAcceptedRemoteChannelFollow :one
-- The remote-video ingestion gate (§2 anti-spam): does ANY local user have an
-- accepted follow edge to this remote actor? An accepted channel follow-back
-- (config-parity W12 federation_auto_follow_back) counts too — the instance
-- asked for that actor's content by following back, so the "we only ingest
-- what we asked for" invariant holds.
SELECT EXISTS (
    SELECT 1 FROM remote_channel_follows f
    WHERE f.remote_actor_url = $1 AND f.state = 'accepted'
    UNION ALL
    SELECT 1 FROM channel_follow_backs fb
    WHERE fb.remote_actor_url = $1 AND fb.state = 'accepted'
);
