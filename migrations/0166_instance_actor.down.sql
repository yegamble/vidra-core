-- Revert 0166. Instance follows and the instance actor's key go away; queued
-- deliveries signed as the instance actor are dropped with them (no other
-- signer could send them). The `peertube` reservation is released.
DROP INDEX IF EXISTS notifications_federation_instance_follower_request_unread_idx;
DROP TABLE IF EXISTS instance_follower_backfills;
DROP TABLE IF EXISTS instance_following;
DROP TABLE IF EXISTS instance_followers;
DELETE FROM federation_deliveries WHERE signing_instance_actor;
ALTER TABLE federation_deliveries DROP COLUMN IF EXISTS signing_instance_actor;
DELETE FROM actor_handles WHERE instance_actor;
ALTER TABLE actor_handles DROP CONSTRAINT actor_handles_one_subject;
ALTER TABLE actor_handles ADD CONSTRAINT actor_handles_one_subject
    CHECK (num_nonnulls(user_id, channel_id) = 1);
ALTER TABLE actor_handles DROP COLUMN IF EXISTS instance_actor;
DROP TABLE IF EXISTS instance_actor_keys;
