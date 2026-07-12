DROP TABLE channel_follow_backs;
DROP INDEX remote_follows_pending_idx;
DROP INDEX remote_follows_id_key;
ALTER TABLE remote_follows DROP COLUMN id;
