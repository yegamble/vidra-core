DROP TABLE IF EXISTS blocklist_allowlist;
DROP TABLE IF EXISTS blocklist_subscriptions;
ALTER TABLE blocked_instances DROP COLUMN IF EXISTS source;
