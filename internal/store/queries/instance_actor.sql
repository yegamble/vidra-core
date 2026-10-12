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
