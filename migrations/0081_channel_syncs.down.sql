-- Down 0081: drop the channel auto-sync tables (W2.C4). channel_sync_seen first
-- (it FK-references channel_syncs).
DROP TABLE IF EXISTS channel_sync_seen;
DROP TABLE IF EXISTS channel_syncs;
