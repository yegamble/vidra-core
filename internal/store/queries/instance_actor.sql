-- The instance actor (migration 0166): PeerTube's `Application` actor, its
-- keypair, its name reservation and the instance-follow collections it serves.

-- name: InstanceActorNameReserved :one
-- Does the instance actor hold this name in the one handle namespace? False on
-- an instance where an account held it before 0166 — that account keeps it,
-- and the instance actor is not served.
SELECT EXISTS (
    SELECT 1 FROM actor_handles WHERE handle_lower = lower($1) AND instance_actor
) AS reserved;

-- name: GetInstanceActorKey :one
SELECT public_key_pem, private_key_pem FROM instance_actor_keys WHERE singleton;

-- name: InsertInstanceActorKeyIfAbsent :execrows
-- Concurrent first requests race to mint; the singleton key keeps exactly one
-- and every caller re-reads the winner.
INSERT INTO instance_actor_keys (public_key_pem, private_key_pem)
VALUES ($1, $2)
ON CONFLICT (singleton) DO NOTHING;

-- name: CountAcceptedInstanceFollowers :one
SELECT count(*)::bigint FROM instance_followers WHERE state = 'accepted';

-- name: CountAcceptedInstanceFollowing :one
SELECT count(*)::bigint FROM instance_following WHERE state = 'accepted';

-- name: InsertInstanceFollowerIfAbsent :execrows
-- A remote instance actor Follows ours. 1 = a new follower (accepted, or
-- pending for an admin under federation_follower_approval); 0 = it was already
-- known, in whatever state the admin left it.
INSERT INTO instance_followers (remote_actor_url, state, follow_activity_url)
VALUES ($1, $2, $3)
ON CONFLICT (remote_actor_url) DO NOTHING;

-- name: GetInstanceFollowerByActor :one
SELECT id, state, follow_activity_url FROM instance_followers WHERE remote_actor_url = $1;

-- name: DeleteInstanceFollowerByActor :execrows
-- Undo{Follow} from the follower. A follower the admin REJECTED stays
-- rejected: an Undo-then-Follow must not walk past the admin's refusal.
DELETE FROM instance_followers WHERE remote_actor_url = $1 AND state <> 'rejected';

-- name: ListAcceptedInstanceFollowerInboxes :many
-- Distinct inboxes (shared inbox preferred) of the instances following ours —
-- the fan-out targets of the instance actor's Announces and of every later
-- Update/Delete of a video they were announced.
SELECT DISTINCT COALESCE(ra.shared_inbox_url, ra.inbox_url) AS inbox
FROM instance_followers f
JOIN remote_actors ra ON ra.actor_url = f.remote_actor_url
WHERE f.state = 'accepted'
  AND COALESCE(ra.shared_inbox_url, ra.inbox_url) <> ''
ORDER BY inbox;

-- name: ListInstanceOutboxVideoIDs :many
-- The instance actor's outbox, newest first: every video a stranger may see
-- that federates at all — public, published, not moderator-blocked, on a
-- channel that has not opted out of ActivityPub (0096). The same predicate
-- AnnounceVideo applies, so the outbox and the push never disagree.
SELECT v.id
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE v.privacy = 'public' AND v.state = 'published' AND c.activitypub_enabled
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
ORDER BY v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountInstanceOutboxVideos :one
-- How many rows ListInstanceOutboxVideoIDs pages over; the predicate must stay
-- identical.
SELECT count(*)::bigint
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE v.privacy = 'public' AND v.state = 'published' AND c.activitypub_enabled
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id);

-- name: ClaimInstanceFollowerBackfill :execrows
-- Atomically claims the new-follower backfill for one remote instance actor:
-- 1 = claimed (never backfilled, or longer ago than the cooldown), 0 = it was
-- backfilled inside the window and gets nothing now. Concurrent Follows race
-- on the primary key, so at most one of them wins.
INSERT INTO instance_follower_backfills (remote_actor_url, last_backfill_at)
VALUES (sqlc.arg('remote_actor_url'), now())
ON CONFLICT (remote_actor_url) DO UPDATE SET last_backfill_at = now()
WHERE instance_follower_backfills.last_backfill_at
      <= now() - make_interval(secs => sqlc.arg('cooldown_seconds')::int);

-- name: ListInstanceFollowers :many
-- The admin's view of the instances following ours, newest first, optionally
-- one state only, with the follower's cached identity.
SELECT f.id, f.remote_actor_url, f.state, f.created_at,
       ra.preferred_username, ra.domain
FROM instance_followers f
JOIN remote_actors ra ON ra.actor_url = f.remote_actor_url
WHERE (sqlc.narg('state')::text IS NULL OR f.state = sqlc.narg('state'))
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountInstanceFollowers :one
SELECT count(*)::bigint FROM instance_followers f
WHERE (sqlc.narg('state')::text IS NULL OR f.state = sqlc.narg('state'));

-- name: AcceptPendingInstanceFollowerByID :one
-- Admin approval of a pending instance follower. Resolved/unknown ids match no
-- row (pgx.ErrNoRows).
UPDATE instance_followers SET state = 'accepted'
WHERE id = $1 AND state = 'pending'
RETURNING remote_actor_url, follow_activity_url;

-- name: RejectInstanceFollowerByID :one
-- Admin rejection of a pending or accepted instance follower. The row stays,
-- 'rejected', so the instance's next Follow is refused too (as on PeerTube).
UPDATE instance_followers SET state = 'rejected'
WHERE id = $1 AND state IN ('pending', 'accepted')
RETURNING remote_actor_url, follow_activity_url;

-- name: DeleteInstanceFollowerByID :one
-- Admin removal, in any state: a removed instance may follow again.
DELETE FROM instance_followers WHERE id = $1
RETURNING remote_actor_url, follow_activity_url, state;

-- name: UpsertInstanceFollowing :one
-- Our instance actor follows a remote one. Idempotent like
-- UpsertRemoteChannelFollow: an existing row is returned untouched (the caller
-- sees its own minted activity id only on a fresh insert, and only then sends
-- a Follow) — except a REJECTED row, which an admin's deliberate re-follow
-- re-arms to pending with the new activity id.
INSERT INTO instance_following (remote_actor_url, host, follow_activity_url)
VALUES ($1, $2, $3)
ON CONFLICT (remote_actor_url) DO UPDATE SET
    state               = CASE WHEN instance_following.state = 'rejected'
                               THEN 'pending' ELSE instance_following.state END,
    follow_activity_url = CASE WHEN instance_following.state = 'rejected'
                               THEN EXCLUDED.follow_activity_url
                               ELSE instance_following.follow_activity_url END
RETURNING id, remote_actor_url, host, state, follow_activity_url, created_at;

-- name: ListInstanceFollowing :many
SELECT f.id, f.remote_actor_url, f.host, f.state, f.created_at, ra.preferred_username
FROM instance_following f
JOIN remote_actors ra ON ra.actor_url = f.remote_actor_url
WHERE (sqlc.narg('state')::text IS NULL OR f.state = sqlc.narg('state'))
ORDER BY f.created_at DESC, f.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountInstanceFollowing :one
SELECT count(*)::bigint FROM instance_following f
WHERE (sqlc.narg('state')::text IS NULL OR f.state = sqlc.narg('state'));

-- name: DeleteInstanceFollowingByID :one
-- Unfollow: local intent wins, the row goes at once; the caller sends the Undo.
DELETE FROM instance_following WHERE id = $1
RETURNING remote_actor_url, follow_activity_url, state;

-- name: AcceptInstanceFollowingByActivity :execrows
-- Inbound Accept of our Follow. The remote_actor_url predicate is the
-- signer-is-the-followed-actor check: an Accept signed by anyone else matches
-- no row.
UPDATE instance_following SET state = 'accepted'
WHERE follow_activity_url = $1 AND remote_actor_url = $2 AND state = 'pending';

-- name: RejectInstanceFollowingByActivity :execrows
-- Inbound Reject of our Follow: kept, 'rejected', so the admin sees it.
UPDATE instance_following SET state = 'rejected'
WHERE follow_activity_url = $1 AND remote_actor_url = $2;

-- name: HasAcceptedInstanceFollowingForHost :one
-- The ingestion gate's instance arm: following an instance asks for every
-- public video it announces, whichever of its actors signs the activity.
SELECT EXISTS (
    SELECT 1 FROM instance_following
    WHERE lower(host) = lower($1) AND state = 'accepted'
) AS following;
