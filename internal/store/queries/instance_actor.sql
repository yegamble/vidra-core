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
