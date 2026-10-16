DROP INDEX IF EXISTS playlists_channel_created_idx;
ALTER TABLE playlists DROP COLUMN IF EXISTS channel_id;
