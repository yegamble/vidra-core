DROP INDEX IF EXISTS remote_video_comments_parent_idx;
ALTER TABLE remote_video_comments DROP COLUMN IF EXISTS parent_object_url;

ALTER TABLE remote_channel_follows DROP CONSTRAINT IF EXISTS remote_channel_follows_state_check;
DELETE FROM remote_channel_follows WHERE state = 'rejected';
ALTER TABLE remote_channel_follows
    ADD CONSTRAINT remote_channel_follows_state_check
    CHECK (state IN ('pending', 'accepted'));

DROP VIEW IF EXISTS remote_actor_block_reach;
DROP TABLE IF EXISTS blocked_remote_actors;

DROP INDEX IF EXISTS remote_actors_attributed_to_idx;
ALTER TABLE remote_actors DROP COLUMN IF EXISTS attributed_to;

DROP TRIGGER IF EXISTS channels_reserve_handle ON channels;
DROP TRIGGER IF EXISTS users_reserve_handle ON users;
DROP FUNCTION IF EXISTS reserve_channel_handle();
DROP FUNCTION IF EXISTS reserve_account_handle();
DROP FUNCTION IF EXISTS backfill_actor_handles();

-- The rename is reversed where it can be: a channel that holds a frozen
-- federated identity goes back to that handle. Aliases and reservations then
-- have nothing left to describe.
UPDATE channels c
   SET handle = a.handle_lower
  FROM channel_handle_aliases a
 WHERE a.channel_id = c.id AND a.is_actor_id;

DROP TABLE IF EXISTS channel_handle_aliases;
DROP INDEX IF EXISTS actor_handles_channel_idx;
DROP INDEX IF EXISTS actor_handles_user_idx;
DROP TABLE IF EXISTS actor_handles;
